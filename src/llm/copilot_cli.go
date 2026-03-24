// Package llm provides Copilot CLI integration.
// This provider uses the GitHub Copilot CLI to make LLM calls,
// using the pre-authenticated CLI (GitHub OAuth/token auth).
package llm

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

	"github.com/officeclaw/src/config"
)

// CopilotCLIProvider implements Provider using the GitHub Copilot CLI.
// The CLI must be pre-authenticated (run `copilot login` once).
type CopilotCLIProvider struct {
	cliPath     string
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
}

// NewCopilotCLIProvider creates a Copilot CLI provider.
func NewCopilotCLIProvider(cfg config.CopilotConfig, defaultTemp float64, timeoutSec int) (*CopilotCLIProvider, error) {
	cliPath := cfg.CLIPath
	if cliPath == "" {
		cliPath = findCopilotCLI()
	}
	if cliPath == "" {
		return nil, fmt.Errorf("Copilot CLI not found. Install GitHub Copilot CLI or set llm.copilot.cli_path")
	}

	if _, err := os.Stat(cliPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Copilot CLI not found at %s", cliPath)
	}

	timeout := time.Duration(timeoutSec) * time.Second
	if timeout == 0 {
		timeout = 180 * time.Second
	}

	log.Printf("[llm/copilot_cli] Using Copilot CLI: %s", cliPath)

	return &CopilotCLIProvider{
		cliPath:     cliPath,
		model:       cfg.Model,
		maxTokens:   cfg.MaxTokens,
		temperature: defaultTemp,
		timeout:     timeout,
	}, nil
}

func (p *CopilotCLIProvider) Name() string { return "copilot-cli" }

// findCopilotCLI locates the GitHub Copilot CLI executable.
func findCopilotCLI() string {
	// Check environment variable first
	if envPath := os.Getenv("COPILOT_CLI_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}

	// Windows default locations
	home, _ := os.UserHomeDir()
	localAppData := os.Getenv("LOCALAPPDATA")
	candidates := []string{
		filepath.Join(localAppData, "Microsoft", "WinGet", "Links", "copilot.exe"),
		filepath.Join(home, ".copilot", "bin", "copilot.exe"),
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// Check system PATH
	if path, err := exec.LookPath("copilot"); err == nil {
		return path
	}
	if path, err := exec.LookPath("copilot.exe"); err == nil {
		return path
	}

	return ""
}

// ChatCompletion sends a request through the Copilot CLI using --output-format json.
func (p *CopilotCLIProvider) ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// Build prompt from messages
	systemPrompt, promptText := buildPromptFromMessages(req.Messages)

	// Inject tool definitions into system prompt (same as Claude CLI)
	if len(req.Tools) > 0 {
		log.Printf("[llm/copilot_cli] Injecting %d tool definitions into system prompt", len(req.Tools))
		systemPrompt = injectToolDefinitions(systemPrompt, req.Tools)
	}

	if strings.TrimSpace(promptText) == "" {
		return nil, fmt.Errorf("no prompt text derived from messages")
	}

	// Build CLI command
	args := []string{
		"-p", promptText,
		"--output-format", "json",
		"--allow-all-tools",
		"-s", // silent (no stats banner)
	}

	if p.model != "" {
		args = append(args, "--model", p.model)
	}

	log.Printf("[llm/copilot_cli] Calling CLI: model=%s, prompt_len=%d, system_len=%d",
		p.model, len(promptText), len(systemPrompt))

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	// Pass system prompt via environment if available
	if systemPrompt != "" {
		// Copilot doesn't have a --system-prompt flag, so we prepend it to the prompt
		// using a clear delimiter
		fullPrompt := fmt.Sprintf("[System Instructions]\n%s\n[End System Instructions]\n\n%s", systemPrompt, promptText)
		// Replace the -p argument
		args[1] = fullPrompt
		cmd = exec.CommandContext(ctx, p.cliPath, args...)
		cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	log.Printf("[llm/copilot_cli] CLI finished in %.1fs (exit=%v)", duration.Seconds(), cmd.ProcessState.ExitCode())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("Copilot CLI timed out after %v", p.timeout)
		}
		stderrText := stderr.String()
		if len(stderrText) > 500 {
			stderrText = stderrText[:500]
		}
		return nil, fmt.Errorf("Copilot CLI error: %s (stderr: %s)", err, stderrText)
	}

	// Parse JSONL output
	content, usedModel, usage, err := parseCopilotJSONL(stdout.String(), p.model)
	if err != nil {
		return nil, err
	}

	// Parse any XML tool calls from the content (same pattern as Claude CLI)
	toolCalls, cleanContent := parseXMLToolCalls(content)

	log.Printf("[llm/copilot_cli] Result: model=%s, content_len=%d, tool_calls=%d",
		usedModel, len(cleanContent), len(toolCalls))

	return &CompletionResponse{
		Content:      cleanContent,
		Role:         "assistant",
		FinishReason: "end_turn",
		Usage:        usage,
		ToolCalls:    toolCalls,
	}, nil
}

// copilotJSONLEvent represents events from Copilot CLI --output-format json.
type copilotJSONLEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	// Top-level fields for result events
	SessionID string `json:"sessionId,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	Usage     *struct {
		PremiumRequests     int `json:"premiumRequests"`
		TotalAPIDurationMs  int `json:"totalApiDurationMs"`
		SessionDurationMs   int `json:"sessionDurationMs"`
	} `json:"usage,omitempty"`
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// copilotAssistantMessage is the data for assistant.message events.
type copilotAssistantMessage struct {
	MessageID    string `json:"messageId"`
	Content      string `json:"content"`
	ToolRequests []struct {
		ToolCallID string          `json:"toolCallId"`
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Type       string          `json:"type"`
	} `json:"toolRequests"`
	OutputTokens int `json:"outputTokens"`
}

// copilotToolsUpdated is the data for session.tools_updated events.
type copilotToolsUpdated struct {
	Model string `json:"model"`
}

// parseCopilotJSONL parses the JSONL output from Copilot CLI.
func parseCopilotJSONL(stdout string, fallbackModel string) (string, string, Usage, error) {
	var assistantTexts []string
	usedModel := fallbackModel
	if usedModel == "" {
		usedModel = "unknown"
	}
	var usage Usage
	var totalOutputTokens int

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event copilotJSONLEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "session.tools_updated":
			// Capture the model name
			var data copilotToolsUpdated
			if json.Unmarshal(event.Data, &data) == nil && data.Model != "" {
				usedModel = data.Model
			}

		case "assistant.message":
			// Full assistant message (non-ephemeral)
			var data copilotAssistantMessage
			if json.Unmarshal(event.Data, &data) == nil {
				if data.Content != "" {
					assistantTexts = append(assistantTexts, data.Content)
				}
				totalOutputTokens += data.OutputTokens
			}

		case "result":
			// Final result with session info
			if event.Usage != nil {
				// Copilot doesn't report input tokens separately
				usage.CompletionTokens = totalOutputTokens
				usage.TotalTokens = totalOutputTokens
			}
			if event.ExitCode != 0 {
				return "", usedModel, usage, fmt.Errorf("Copilot CLI exited with code %d", event.ExitCode)
			}
		}
	}

	var content string
	if len(assistantTexts) > 0 {
		content = strings.Join(assistantTexts, "\n\n")
	} else {
		content = strings.TrimSpace(stdout)
		log.Printf("[llm/copilot_cli] Warning: No structured content found, using raw stdout")
	}

	return content, usedModel, usage, nil
}

// ValidateCopilotCLI checks if the Copilot CLI is available.
func ValidateCopilotCLI(cliPath string) error {
	if cliPath == "" {
		cliPath = findCopilotCLI()
	}
	if cliPath == "" {
		return fmt.Errorf("Copilot CLI not found")
	}

	cmd := exec.Command(cliPath, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Copilot CLI validation failed: %w", err)
	}

	return nil
}

// FindCopilotCLI is the exported version of findCopilotCLI for use by agent package.
func FindCopilotCLI() string {
	return findCopilotCLI()
}

// CopilotToolCall represents a tool call from Copilot's native JSONL output.
// Used by the CopilotAgent for direct tool execution.
type CopilotToolCall struct {
	ToolCallID string
	Name       string
	Arguments  string // JSON string
}

// ParseCopilotToolCalls extracts native Copilot tool calls from JSONL output.
// Returns tool calls from assistant.message events with toolRequests.
func ParseCopilotToolCalls(stdout string) []CopilotToolCall {
	var calls []CopilotToolCall

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event copilotJSONLEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if event.Type == "assistant.message" {
			var data copilotAssistantMessage
			if json.Unmarshal(event.Data, &data) == nil {
				for _, tr := range data.ToolRequests {
					args := string(tr.Arguments)
					// Ensure arguments is a JSON string
					if !strings.HasPrefix(args, "{") {
						argsJSON, _ := json.Marshal(tr.Arguments)
						args = string(argsJSON)
					}
					calls = append(calls, CopilotToolCall{
						ToolCallID: tr.ToolCallID,
						Name:       tr.Name,
						Arguments:  args,
					})
				}
			}
		}
	}

	return calls
}

// CopilotSessionInfo holds session metadata from a Copilot CLI invocation.
type CopilotSessionInfo struct {
	SessionID string
	Model     string
	ExitCode  int
}

// ParseCopilotSessionInfo extracts session info from JSONL output.
func ParseCopilotSessionInfo(stdout string) CopilotSessionInfo {
	info := CopilotSessionInfo{}

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event copilotJSONLEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "session.tools_updated":
			var data copilotToolsUpdated
			if json.Unmarshal(event.Data, &data) == nil && data.Model != "" {
				info.Model = data.Model
			}
		case "result":
			info.SessionID = event.SessionID
			info.ExitCode = event.ExitCode
		}
	}

	return info
}

