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
type ClaudeAgent struct {
	cliPath       string
	workingFolder string
	waClient      *whatsapp.Client
	logger        *log.Logger
	timeout       time.Duration
}

// ClaudeAgentConfig holds configuration for the Claude CLI agent.
type ClaudeAgentConfig struct {
	CLIPath       string           // Path to Claude CLI (auto-detected if empty)
	WorkingFolder string           // Working directory for Claude CLI
	WAClient      *whatsapp.Client // WhatsApp client for sending replies
	Logger        *log.Logger
	Timeout       time.Duration    // Timeout for CLI execution
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

	return &ClaudeAgent{
		cliPath:       cliPath,
		workingFolder: workingFolder,
		waClient:      cfg.WAClient,
		logger:        cfg.Logger,
		timeout:       timeout,
	}, nil
}

// HandleMessage processes a message using Claude CLI as an autonomous agent.
func (a *ClaudeAgent) HandleMessage(ctx context.Context, msg whatsapp.IncomingMessage) {
	a.logger.Printf("[claude-agent] Processing message from %s: %s", msg.SenderJID, truncateForLog(msg.Body, 100))

	// Build the prompt with context about the WhatsApp message
	prompt := fmt.Sprintf(`You received a WhatsApp message. After processing, you MUST send a reply.

From: %s
Chat ID: %s
Message: %s

Process this request and provide a helpful response. When done, output your response text that should be sent back to the user.`,
		msg.SenderJID, msg.ChatJID, msg.Body)

	// Execute Claude CLI with auto-approval
	response, err := a.executeClaudeCLI(ctx, prompt)
	if err != nil {
		a.logger.Printf("[claude-agent] CLI error: %v", err)
		response = fmt.Sprintf("Sorry, I encountered an error: %v", err)
	}

	// Send response back via WhatsApp
	if err := a.waClient.SendMessage(ctx, msg.ChatJID, response); err != nil {
		a.logger.Printf("[claude-agent] Failed to send reply: %v", err)
	} else {
		a.logger.Printf("[claude-agent] Sent reply to %s (%d chars)", msg.ChatJID, len(response))
	}
}

// executeClaudeCLI runs Claude CLI with the given prompt and returns the response.
func (a *ClaudeAgent) executeClaudeCLI(ctx context.Context, prompt string) (string, error) {
	// Use --dangerously-skip-permissions to auto-approve all tool requests
	// Use --print to get just the final output
	args := []string{
		"-p",                              // Print mode (non-interactive)
		"--dangerously-skip-permissions",  // Auto-approve all tool requests
		"--output-format", "text",         // Plain text output
	}

	workDir := a.workingFolder
	if workDir == "" {
		workDir = "."
	}
	a.logger.Printf("[claude-agent] Executing CLI with auto-approval in folder: %s", workDir)

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.cliPath, args...)
	cmd.Dir = workDir // Set working directory
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

	return strings.TrimSpace(stdout.String()), nil
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

// streamJSONResult represents the result from stream-json output.
type streamJSONResult struct {
	Type   string `json:"type"`
	Result string `json:"result"`
}

// parseStreamJSONResult extracts the final result from stream-json output.
func parseStreamJSONResult(output string) string {
	var lastResult string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var result streamJSONResult
		if err := json.Unmarshal([]byte(line), &result); err == nil {
			if result.Type == "result" && result.Result != "" {
				lastResult = result.Result
			}
		}
	}
	if lastResult != "" {
		return lastResult
	}
	return output
}
