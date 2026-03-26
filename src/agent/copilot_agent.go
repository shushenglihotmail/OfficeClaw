// Package agent implements the Copilot CLI agent mode.
// This allows direct invocation of Copilot CLI as an autonomous agent,
// similar to the Claude CLI agent (OCC: mode).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/memory"
	"github.com/officeclaw/src/pending"
	"github.com/officeclaw/src/telegram"
)

// CopilotAgent handles messages by invoking Copilot CLI directly as an agent.
// It maintains conversation context per-chat using --resume with session IDs.
// Thread-safe for concurrent messages from different chats.
type CopilotAgent struct {
	cliPath        string
	model          string
	workingFolder  string
	officeClawPath string // Path to OfficeClaw executable for MCP server
	tgClient       *telegram.Client
	memoryClient   *memory.Client   // Optional
	pendingQueue   *pending.Queue   // Optional
	logger         *log.Logger
	timeout        time.Duration
	resetKeyword   string

	// Per-chat session tracking (thread-safe)
	sessions    map[string]string // chatJID -> sessionID
	chatModels  map[string]string // chatJID -> model override
	chatEfforts map[string]string // chatJID -> reasoning effort override
	mu          sync.RWMutex

	// Graceful shutdown
	wg     sync.WaitGroup
	cancel context.CancelFunc
	ctx    context.Context
}

// CopilotAgentConfig holds configuration for the Copilot CLI agent.
type CopilotAgentConfig struct {
	CLIPath       string           // Path to Copilot CLI (auto-detected if empty)
	Model         string           // Model to use (empty = default)
	WorkingFolder string           // Working directory for Copilot CLI
	TGClient      *telegram.Client // Telegram client for sending replies
	MemoryClient  *memory.Client   // Optional: memory service client
	PendingQueue  *pending.Queue   // Optional: queue for unsent messages
	Logger        *log.Logger
	Timeout       time.Duration
	ResetKeyword  string // Keyword to reset session (default: "reset")
}

// NewCopilotAgent creates a new Copilot CLI agent.
func NewCopilotAgent(cfg CopilotAgentConfig) (*CopilotAgent, error) {
	cliPath := cfg.CLIPath
	if cliPath == "" {
		cliPath = llm.FindCopilotCLI()
	}
	if cliPath == "" {
		return nil, fmt.Errorf("Copilot CLI not found")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	workingFolder := cfg.WorkingFolder
	if workingFolder != "" {
		if info, err := os.Stat(workingFolder); err != nil || !info.IsDir() {
			if cwd, err := os.Getwd(); err == nil {
				workingFolder = cwd
			} else {
				workingFolder = ""
			}
		}
	}

	resetKeyword := cfg.ResetKeyword
	if resetKeyword == "" {
		resetKeyword = "reset"
	}

	officeClawPath, err := os.Executable()
	if err != nil {
		cfg.Logger.Printf("[copilot-agent] Warning: could not get OfficeClaw path for MCP: %v", err)
	}

	agentCtx, agentCancel := context.WithCancel(context.Background())

	return &CopilotAgent{
		cliPath:        cliPath,
		model:          cfg.Model,
		workingFolder:  workingFolder,
		officeClawPath: officeClawPath,
		tgClient:       cfg.TGClient,
		memoryClient:   cfg.MemoryClient,
		pendingQueue:   cfg.PendingQueue,
		logger:         cfg.Logger,
		timeout:        timeout,
		resetKeyword:   resetKeyword,
		sessions:       make(map[string]string),
		chatModels:     make(map[string]string),
		chatEfforts:    make(map[string]string),
		cancel:         agentCancel,
		ctx:            agentCtx,
	}, nil
}

func (a *CopilotAgent) getSessionID(chatJID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[chatJID]
}

func (a *CopilotAgent) setSessionID(chatJID, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[chatJID] = sessionID
}

func (a *CopilotAgent) clearSession(chatJID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, chatJID)
}

// HandleMessage processes a message using Copilot CLI as an autonomous agent.
func (a *CopilotAgent) HandleMessage(ctx context.Context, msg telegram.IncomingMessage) {
	a.logger.Printf("[copilot-agent] Processing message from %s (chat: %s): %s",
		msg.SenderID, msg.ChatID, truncateForLog(msg.Body, 100))

	// Check for slash commands and legacy reset keyword
	if cmd := ParseCommand(msg.Body); cmd != nil {
		a.handleCommand(ctx, msg.ChatID, cmd)
		return
	}
	if strings.EqualFold(strings.TrimSpace(msg.Body), a.resetKeyword) {
		a.handleCommand(ctx, msg.ChatID, &Command{Name: "reset"})
		return
	}

	// Build prompt
	prompt := fmt.Sprintf(`You received a Telegram message. Process it and provide a helpful response.

From: %s
Message: %s

Respond directly to the user's message.`,
		msg.SenderID, msg.Body)

	// Log to memory service
	sessionID := a.getSessionID(msg.ChatID)
	memorySessionID := sessionID
	if memorySessionID == "" {
		memorySessionID = "copilot-" + msg.ChatID
	}
	if a.memoryClient != nil {
		go func() {
			if err := a.memoryClient.WriteDaily(ctx, "user", msg.Body, memorySessionID); err != nil {
				a.logger.Printf("[copilot-agent] Failed to log user message to memory: %v", err)
			}
		}()
	}

	// Execute Copilot CLI
	a.wg.Add(1)
	defer a.wg.Done()
	execCtx, execCancel := context.WithCancel(a.ctx)
	defer execCancel()
	response, err := a.executeCopilotCLI(execCtx, msg.ChatID, prompt)
	if err != nil {
		a.logger.Printf("[copilot-agent] CLI error: %v", err)
		response = fmt.Sprintf("Sorry, I encountered an error: %v", err)
	}

	// Log response to memory
	if a.memoryClient != nil && response != "" {
		finalSessionID := a.getSessionID(msg.ChatID)
		if finalSessionID == "" {
			finalSessionID = memorySessionID
		}
		go func() {
			if err := a.memoryClient.WriteDaily(ctx, "assistant", response, finalSessionID); err != nil {
				a.logger.Printf("[copilot-agent] Failed to log assistant message to memory: %v", err)
			}
		}()
	}

	a.sendReply(ctx, msg.ChatID, response)
}

// executeCopilotCLI runs Copilot CLI with the given prompt and returns the response.
func (a *CopilotAgent) executeCopilotCLI(ctx context.Context, chatJID, prompt string) (string, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--allow-all",          // Auto-approve all tool/path/url requests
		"-s",                   // Silent mode (no stats banner)
		"--no-custom-instructions", // Don't load AGENTS.md (we provide our own prompt)
	}

	// Add MCP server configuration for OfficeClaw tools
	if a.officeClawPath != "" {
		mcpConfig := fmt.Sprintf(`{"mcpServers":{"officeclaw":{"command":"%s","args":["mcp","serve"]}}}`,
			strings.ReplaceAll(a.officeClawPath, `\`, `\\`))
		args = append(args, "--additional-mcp-config", mcpConfig)
		a.logger.Printf("[copilot-agent] MCP server configured: %s", a.officeClawPath)
	}

	// Apply model: per-chat override takes priority, then config default
	if model := a.getChatModel(chatJID); model != "" {
		args = append(args, "--model", model)
	} else if a.model != "" {
		args = append(args, "--model", a.model)
	}

	// Apply reasoning effort if set
	if effort := a.getChatEffort(chatJID); effort != "" {
		args = append(args, "--reasoning-effort", effort)
	}

	// Resume existing session if available
	sessionID := a.getSessionID(chatJID)
	if sessionID != "" {
		args = append(args, fmt.Sprintf("--resume=%s", sessionID))
	}

	workDir := a.workingFolder
	if workDir == "" {
		workDir = "."
	}

	sessionInfo := sessionID
	if sessionInfo == "" {
		sessionInfo = "(new)"
	}
	a.logger.Printf("[copilot-agent] Executing CLI for chat %s (session: %s, folder: %s)", chatJID, sessionInfo, workDir)

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	a.logger.Printf("[copilot-agent] CLI finished in %.1fs", duration.Seconds())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timed out after %v", a.timeout)
		}
		return "", fmt.Errorf("CLI error: %w (stderr: %s)", err, truncateForLog(stderr.String(), 200))
	}

	// Parse JSONL output
	response, newSessionID := parseCopilotOutput(stdout.String())

	if newSessionID != "" && newSessionID != sessionID {
		a.logger.Printf("[copilot-agent] Captured session ID for chat %s: %s", chatJID, newSessionID)
		a.setSessionID(chatJID, newSessionID)
	}

	return response, nil
}

// copilotEvent matches the JSONL event structure from Copilot CLI.
type copilotEvent struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	SessionID string          `json:"sessionId,omitempty"`
	ExitCode  int             `json:"exitCode,omitempty"`
	Ephemeral bool            `json:"ephemeral,omitempty"`
}

// copilotMessageData matches the assistant.message data structure.
type copilotMessageData struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
	Phase     string `json:"phase"` // "final_answer" for the last message
}

// parseCopilotOutput extracts the response text and session ID from JSONL output.
func parseCopilotOutput(output string) (string, string) {
	var assistantTexts []string
	var sessionID string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event copilotEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant.message":
			if event.Ephemeral {
				continue
			}
			var data copilotMessageData
			if json.Unmarshal(event.Data, &data) == nil && data.Content != "" {
				assistantTexts = append(assistantTexts, data.Content)
			}

		case "result":
			if event.SessionID != "" {
				sessionID = event.SessionID
			}
		}
	}

	var response string
	if len(assistantTexts) > 0 {
		// Take the last assistant message (final answer after tool calls)
		response = assistantTexts[len(assistantTexts)-1]
	} else {
		response = strings.TrimSpace(output)
	}

	return response, sessionID
}

// handleCommand processes a slash command for the Copilot agent.
func (a *CopilotAgent) handleCommand(ctx context.Context, chatJID string, cmd *Command) {
	switch cmd.Name {
	case "reset":
		oldSession := a.getSessionID(chatJID)
		a.clearSession(chatJID)
		a.logger.Printf("[copilot-agent] Session reset for chat %s: %s -> (new)", chatJID, oldSession)
		a.sendReply(ctx, chatJID, "Copilot session restarted. Conversation context has been cleared.")

	case "model":
		if cmd.Args == "" {
			current := a.getChatModel(chatJID)
			if current == "" {
				current = a.model
			}
			if current == "" {
				current = "(default)"
			}
			effort := a.getChatEffort(chatJID)
			if effort == "" {
				effort = "(default)"
			}
			a.sendReply(ctx, chatJID, fmt.Sprintf("Current model: %s\nReasoning effort: %s\nUse /model <name> [effort] to switch.\nEffort levels: low, medium, high, xhigh", current, effort))
		} else {
			model, effort := parseModelArgs(cmd.Args)
			a.setChatModel(chatJID, model)
			if effort != "" {
				a.setChatEffort(chatJID, effort)
				a.logger.Printf("[copilot-agent] Model set to %q effort %q for chat %s", model, effort, chatJID)
				a.sendReply(ctx, chatJID, fmt.Sprintf("Model switched to: %s (effort: %s)", model, effort))
			} else {
				a.setChatEffort(chatJID, "") // clear effort when switching model without specifying
				a.logger.Printf("[copilot-agent] Model set to %q for chat %s", model, chatJID)
				a.sendReply(ctx, chatJID, fmt.Sprintf("Model switched to: %s", model))
			}
		}

	case "effort":
		if cmd.Args == "" {
			effort := a.getChatEffort(chatJID)
			if effort == "" {
				effort = "(default)"
			}
			a.sendReply(ctx, chatJID, fmt.Sprintf("Current reasoning effort: %s\nUse /effort <level> to change.\nLevels: low, medium, high, xhigh", effort))
		} else {
			level := strings.ToLower(strings.TrimSpace(cmd.Args))
			a.setChatEffort(chatJID, level)
			a.logger.Printf("[copilot-agent] Effort set to %q for chat %s", level, chatJID)
			a.sendReply(ctx, chatJID, fmt.Sprintf("Reasoning effort set to: %s", level))
		}

	case "models":
		known := DefaultKnownModels()
		current := a.getChatModel(chatJID)
		if current == "" {
			current = a.model
		}
		a.sendReply(ctx, chatJID, FormatModelList("Copilot", known.Copilot, current))

	case "help":
		a.sendReply(ctx, chatJID, CommandHelpText("OCCO"))

	default:
		a.sendReply(ctx, chatJID, fmt.Sprintf("Unknown command: /%s\n\n%s", cmd.Name, CommandHelpText("OCCO")))
	}
}

// getChatModel returns the model override for a chat, or empty for default.
func (a *CopilotAgent) getChatModel(chatJID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.chatModels[chatJID]
}

// setChatModel sets a model override for a chat.
func (a *CopilotAgent) setChatModel(chatJID, model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chatModels[chatJID] = model
}

// getChatEffort returns the reasoning effort override for a chat, or empty for default.
func (a *CopilotAgent) getChatEffort(chatJID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.chatEfforts[chatJID]
}

// setChatEffort sets a reasoning effort override for a chat.
func (a *CopilotAgent) setChatEffort(chatJID, effort string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chatEfforts[chatJID] = effort
}

// sendReply sends a message back via Telegram.
func (a *CopilotAgent) sendReply(ctx context.Context, chatID, message string) {
	if err := a.tgClient.SendMessage(ctx, chatID, message); err != nil {
		a.logger.Printf("[copilot-agent] Failed to send reply: %v", err)
		if a.pendingQueue != nil {
			a.pendingQueue.Add(chatID, message)
		}
	} else {
		a.logger.Printf("[copilot-agent] Sent reply to %s (%d chars)", chatID, len(message))
	}
}

// Stop cancels running CLI sessions and waits for them to finish.
func (a *CopilotAgent) Stop() {
	a.logger.Printf("[copilot-agent] Stopping: cancelling running CLI sessions")
	a.cancel()

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		a.logger.Printf("[copilot-agent] All CLI sessions finished")
	case <-time.After(30 * time.Second):
		a.logger.Printf("[copilot-agent] Timeout waiting for CLI sessions")
	}
}
