// Package agent implements the Claude CLI agent mode.
// This allows direct invocation of Claude CLI as an autonomous agent,
// bypassing the OfficeClaw agent loop.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/officeclaw/src/memory"
	"github.com/officeclaw/src/whatsapp"
)

// ClaudeAgent handles messages by invoking Claude CLI directly as an agent.
// It maintains conversation context per-chat using --resume with session IDs.
// Thread-safe for concurrent messages from different chats.
type ClaudeAgent struct {
	cliPath         string
	workingFolder   string
	officeClawPath  string // Path to OfficeClaw executable for MCP server
	waClient        *whatsapp.Client
	memoryClient    *memory.Client // Optional: nil if memory service not available
	logger          *log.Logger
	timeout         time.Duration
	resetKeyword    string // Keyword to reset session (e.g., "reset")

	// Per-chat session tracking (thread-safe)
	sessions map[string]string // chatJID -> sessionID
	mu       sync.RWMutex
}

// ClaudeAgentConfig holds configuration for the Claude CLI agent.
type ClaudeAgentConfig struct {
	CLIPath       string           // Path to Claude CLI (auto-detected if empty)
	WorkingFolder string           // Working directory for Claude CLI
	WAClient      *whatsapp.Client // WhatsApp client for sending replies
	MemoryClient  *memory.Client   // Optional: memory service client for logging
	Logger        *log.Logger
	Timeout       time.Duration // Timeout for CLI execution
	ResetKeyword  string        // Keyword to reset session (default: "reset")
}

// NewClaudeAgent creates a new Claude CLI agent.
func NewClaudeAgent(cfg ClaudeAgentConfig) (*ClaudeAgent, error) {
	cliPath := cfg.CLIPath
	if cliPath == "" {
		cliPath = findClaudeCLI()
	}
	if cliPath == "" {
		return nil, fmt.Errorf("Claude CLI not found")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	// Validate working folder - fall back to executable's current folder if not valid
	workingFolder := cfg.WorkingFolder
	if workingFolder != "" {
		if info, err := os.Stat(workingFolder); err != nil || !info.IsDir() {
			// Working folder doesn't exist or isn't a directory, use current folder
			if cwd, err := os.Getwd(); err == nil {
				workingFolder = cwd
			} else {
				workingFolder = ""
			}
		}
	}

	// Default reset keyword
	resetKeyword := cfg.ResetKeyword
	if resetKeyword == "" {
		resetKeyword = "reset"
	}

	// Find OfficeClaw executable path for MCP server
	officeClawPath, err := os.Executable()
	if err != nil {
		cfg.Logger.Printf("[claude-agent] Warning: could not get OfficeClaw path for MCP: %v", err)
	}

	return &ClaudeAgent{
		cliPath:        cliPath,
		workingFolder:  workingFolder,
		officeClawPath: officeClawPath,
		waClient:       cfg.WAClient,
		memoryClient:   cfg.MemoryClient,
		logger:         cfg.Logger,
		timeout:        timeout,
		resetKeyword:   resetKeyword,
		sessions:       make(map[string]string), // Per-chat session tracking
	}, nil
}

// getSessionID returns the session ID for a chat (thread-safe).
func (a *ClaudeAgent) getSessionID(chatJID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[chatJID]
}

// setSessionID stores the session ID for a chat (thread-safe).
func (a *ClaudeAgent) setSessionID(chatJID, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[chatJID] = sessionID
}

// clearSession removes the session for a chat (thread-safe).
func (a *ClaudeAgent) clearSession(chatJID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, chatJID)
}


// HandleMessage processes a message using Claude CLI as an autonomous agent.
// Conversation context is maintained per-chat using --resume with session IDs.
// Send the reset keyword (default: "reset") to start a new session for that chat.
func (a *ClaudeAgent) HandleMessage(ctx context.Context, msg whatsapp.IncomingMessage) {
	a.logger.Printf("[claude-agent] Processing message from %s (chat: %s): %s",
		msg.SenderJID, msg.ChatJID, truncateForLog(msg.Body, 100))

	// Check for reset keyword (case-insensitive)
	if strings.EqualFold(strings.TrimSpace(msg.Body), a.resetKeyword) {
		oldSession := a.getSessionID(msg.ChatJID)
		a.clearSession(msg.ChatJID)
		a.logger.Printf("[claude-agent] Session reset for chat %s: %s -> (new)", msg.ChatJID, oldSession)
		a.sendReply(ctx, msg.ChatJID, "Session restarted. Conversation context has been cleared.")
		return
	}

	// Build the prompt with context about the WhatsApp message
	prompt := fmt.Sprintf(`You received a WhatsApp message. Process it and provide a helpful response.

From: %s
Message: %s

Respond directly to the user's message.`,
		msg.SenderJID, msg.Body)

	// Log user message to memory service (async)
	// Use existing session ID if available, otherwise use chat JID as prefix
	sessionID := a.getSessionID(msg.ChatJID)
	memorySessionID := sessionID
	if memorySessionID == "" {
		memorySessionID = "occ-" + msg.ChatJID // Temporary until we get Claude's session ID
	}
	if a.memoryClient != nil {
		go func() {
			if err := a.memoryClient.WriteDaily(ctx, "user", msg.Body, memorySessionID); err != nil {
				a.logger.Printf("[claude-agent] Failed to log user message to memory: %v", err)
			}
		}()
	}

	// Execute Claude CLI with --resume to maintain per-chat session context
	response, err := a.executeClaudeCLI(ctx, msg.ChatJID, prompt)
	if err != nil {
		a.logger.Printf("[claude-agent] CLI error: %v", err)
		response = fmt.Sprintf("Sorry, I encountered an error: %v", err)
	}

	// Log assistant response to memory service (async)
	// Use updated session ID (may have been captured from CLI output)
	if a.memoryClient != nil && response != "" {
		finalSessionID := a.getSessionID(msg.ChatJID)
		if finalSessionID == "" {
			finalSessionID = memorySessionID
		}
		go func() {
			if err := a.memoryClient.WriteDaily(ctx, "assistant", response, finalSessionID); err != nil {
				a.logger.Printf("[claude-agent] Failed to log assistant message to memory: %v", err)
			}
		}()
	}

	a.sendReply(ctx, msg.ChatJID, response)
}

// executeClaudeCLI runs Claude CLI with the given prompt and returns the response.
// Uses --resume to maintain per-chat conversation context across invocations.
// On first call (no session for this chat), captures session ID for subsequent calls.
// Automatically configures OfficeClaw as an MCP server so Claude CLI has access to tools.
func (a *ClaudeAgent) executeClaudeCLI(ctx context.Context, chatJID, prompt string) (string, error) {
	args := []string{
		"-p",                             // Print mode (non-interactive)
		"--dangerously-skip-permissions", // Auto-approve all tool requests
		"--output-format", "stream-json", // JSON output to capture session ID
		"--verbose",                      // Required for stream-json with -p
	}

	// Add MCP server configuration if OfficeClaw path is available
	if a.officeClawPath != "" {
		// Create MCP config JSON to expose OfficeClaw tools to Claude CLI
		mcpConfig := fmt.Sprintf(`{"mcpServers":{"officeclaw":{"command":"%s","args":["mcp","serve"]}}}`,
			strings.ReplaceAll(a.officeClawPath, `\`, `\\`)) // Escape backslashes for JSON
		args = append(args, "--mcp-config", mcpConfig)
		a.logger.Printf("[claude-agent] MCP server configured: %s", a.officeClawPath)
	}

	// Only use --resume if we have an existing session for this chat
	sessionID := a.getSessionID(chatJID)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	workDir := a.workingFolder
	if workDir == "" {
		workDir = "."
	}

	sessionInfo := sessionID
	if sessionInfo == "" {
		sessionInfo = "(new)"
	}
	a.logger.Printf("[claude-agent] Executing CLI for chat %s (session: %s, folder: %s)", chatJID, sessionInfo, workDir)

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	a.logger.Printf("[claude-agent] CLI finished in %.1fs", duration.Seconds())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timed out after %v", a.timeout)
		}
		return "", fmt.Errorf("CLI error: %w (stderr: %s)", err, truncateForLog(stderr.String(), 200))
	}

	// Parse stream-json output to get response and session ID
	response, newSessionID := parseStreamJSONOutput(stdout.String())

	// Save session ID for future calls from this chat
	if newSessionID != "" && newSessionID != sessionID {
		a.logger.Printf("[claude-agent] Captured session ID for chat %s: %s", chatJID, newSessionID)
		a.setSessionID(chatJID, newSessionID)
	}

	return response, nil
}

// streamJSONEvent represents events from Claude CLI stream-json output.
type streamJSONEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

// parseStreamJSONOutput extracts the response text and session ID from stream-json output.
func parseStreamJSONOutput(output string) (string, string) {
	var assistantTexts []string
	var sessionID string
	var resultText string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event streamJSONEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant":
			for _, block := range event.Message.Content {
				if block.Type == "text" && block.Text != "" {
					assistantTexts = append(assistantTexts, block.Text)
				}
			}
		case "result":
			resultText = event.Result
			if event.SessionID != "" {
				sessionID = event.SessionID
			}
		}
	}

	// Prefer assistant texts, fall back to result
	var response string
	if len(assistantTexts) > 0 {
		response = strings.Join(assistantTexts, "\n\n")
	} else if resultText != "" {
		response = resultText
	} else {
		response = strings.TrimSpace(output)
	}

	return response, sessionID
}

// sendReply sends a message back via WhatsApp.
func (a *ClaudeAgent) sendReply(ctx context.Context, chatJID, message string) {
	if err := a.waClient.SendMessage(ctx, chatJID, message); err != nil {
		a.logger.Printf("[claude-agent] Failed to send reply: %v", err)
	} else {
		a.logger.Printf("[claude-agent] Sent reply to %s (%d chars)", chatJID, len(message))
	}
}

// Stop is a no-op for this implementation (no persistent process).
func (a *ClaudeAgent) Stop() {
	// Nothing to stop - each request is a separate CLI invocation
}

// findClaudeCLI locates the Claude CLI executable.
func findClaudeCLI() string {
	// Check environment variable first
	if envPath := os.Getenv("CLAUDE_CLI_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}

	// Windows default locations
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".claude-cli", "currentVersion", "claude.exe"),
		filepath.Join(home, ".claude-cli", "claude.exe"),
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// Check system PATH
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	if path, err := exec.LookPath("claude.exe"); err == nil {
		return path
	}

	return ""
}

// truncateForLog shortens a string for logging.
func truncateForLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
