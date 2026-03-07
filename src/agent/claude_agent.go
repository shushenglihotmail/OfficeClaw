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
	"time"

	"github.com/officeclaw/src/whatsapp"
)

// ClaudeAgent handles messages by invoking Claude CLI directly as an agent.
// It maintains conversation context across requests using --resume with a session ID.
type ClaudeAgent struct {
	cliPath       string
	workingFolder string
	waClient      *whatsapp.Client
	logger        *log.Logger
	timeout       time.Duration
	resetKeyword  string // Keyword to reset session (e.g., "reset")
	sessionID     string // Session ID for --resume flag (empty until first successful call)
}

// ClaudeAgentConfig holds configuration for the Claude CLI agent.
type ClaudeAgentConfig struct {
	CLIPath       string           // Path to Claude CLI (auto-detected if empty)
	WorkingFolder string           // Working directory for Claude CLI
	WAClient      *whatsapp.Client // WhatsApp client for sending replies
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

	return &ClaudeAgent{
		cliPath:       cliPath,
		workingFolder: workingFolder,
		waClient:      cfg.WAClient,
		logger:        cfg.Logger,
		timeout:       timeout,
		resetKeyword:  resetKeyword,
		sessionID:     "", // Empty until first successful call
	}, nil
}


// HandleMessage processes a message using Claude CLI as an autonomous agent.
// Conversation context is maintained across messages using --resume.
// Send the reset keyword (default: "reset") to start a new session.
func (a *ClaudeAgent) HandleMessage(ctx context.Context, msg whatsapp.IncomingMessage) {
	a.logger.Printf("[claude-agent] Processing message from %s: %s", msg.SenderJID, truncateForLog(msg.Body, 100))

	// Check for reset keyword (case-insensitive)
	if strings.EqualFold(strings.TrimSpace(msg.Body), a.resetKeyword) {
		oldSession := a.sessionID
		a.sessionID = "" // Clear session - next call will start fresh
		a.logger.Printf("[claude-agent] Session reset: %s -> (new)", oldSession)
		a.sendReply(ctx, msg.ChatJID, "Session restarted. Conversation context has been cleared.")
		return
	}

	// Build the prompt with context about the WhatsApp message
	prompt := fmt.Sprintf(`You received a WhatsApp message. Process it and provide a helpful response.

From: %s
Message: %s

Respond directly to the user's message.`,
		msg.SenderJID, msg.Body)

	// Execute Claude CLI with --resume to maintain session context
	response, err := a.executeClaudeCLI(ctx, prompt)
	if err != nil {
		a.logger.Printf("[claude-agent] CLI error: %v", err)
		response = fmt.Sprintf("Sorry, I encountered an error: %v", err)
	}

	a.sendReply(ctx, msg.ChatJID, response)
}

// executeClaudeCLI runs Claude CLI with the given prompt and returns the response.
// Uses --resume to maintain conversation context across invocations.
// On first call (no session), captures session ID from response for subsequent calls.
func (a *ClaudeAgent) executeClaudeCLI(ctx context.Context, prompt string) (string, error) {
	args := []string{
		"-p",                             // Print mode (non-interactive)
		"--dangerously-skip-permissions", // Auto-approve all tool requests
		"--output-format", "stream-json", // JSON output to capture session ID
		"--verbose",                      // Required for stream-json with -p
	}

	// Only use --resume if we have an existing session
	if a.sessionID != "" {
		args = append(args, "--resume", a.sessionID)
	}

	workDir := a.workingFolder
	if workDir == "" {
		workDir = "."
	}

	sessionInfo := a.sessionID
	if sessionInfo == "" {
		sessionInfo = "(new)"
	}
	a.logger.Printf("[claude-agent] Executing CLI (session: %s, folder: %s)", sessionInfo, workDir)

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
	response, sessionID := parseStreamJSONOutput(stdout.String())

	// Save session ID for future calls
	if sessionID != "" && a.sessionID != sessionID {
		a.logger.Printf("[claude-agent] Captured session ID: %s", sessionID)
		a.sessionID = sessionID
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
