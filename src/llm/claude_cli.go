// Package llm provides Claude CLI integration.
// This provider uses the pre-authenticated Claude Code CLI to make LLM calls,
// bypassing the need for API keys (uses organization SSO auth).
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
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/officeclaw/src/config"
)

// ClaudeCLIProvider implements Provider using the Claude Code CLI.
// The CLI must be pre-authenticated via SSO (run `claude` once to authenticate).
type ClaudeCLIProvider struct {
	cliPath     string
	model       string
	maxTokens   int
	temperature float64
	timeout     time.Duration
}

// NewClaudeCLIProvider creates a Claude CLI provider.
// It auto-discovers the CLI path if not specified.
func NewClaudeCLIProvider(cfg config.AnthropicConfig, defaultTemp float64, timeoutSec int) (*ClaudeCLIProvider, error) {
	cliPath := cfg.CLIPath
	if cliPath == "" {
		cliPath = findClaudeCLI()
	}

	if cliPath == "" {
		return nil, fmt.Errorf("Claude CLI not found. Install Claude Code CLI or set llm.anthropic.cli_path")
	}

	// Verify CLI exists
	if _, err := os.Stat(cliPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Claude CLI not found at %s", cliPath)
	}

	log.Printf("[llm/claude_cli] Using Claude CLI: %s", cliPath)

	return &ClaudeCLIProvider{
		cliPath:     cliPath,
		model:       cfg.Model,
		maxTokens:   cfg.MaxTokens,
		temperature: defaultTemp,
		timeout:     time.Duration(timeoutSec) * time.Second,
	}, nil
}

func (p *ClaudeCLIProvider) Name() string { return "claude-cli" }

// findClaudeCLI locates the Claude Code CLI executable.
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

// ChatCompletion sends a chat completion request through the Claude CLI.
func (p *ClaudeCLIProvider) ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// Build prompt from messages
	systemPrompt, promptText := buildPromptFromMessages(req.Messages)

	// Inject tool definitions into the system prompt so the LLM knows
	// what tools are available and how to call them via XML format.
	if len(req.Tools) > 0 {
		log.Printf("[llm/claude_cli] Injecting %d tool definitions into system prompt", len(req.Tools))
		systemPrompt = injectToolDefinitions(systemPrompt, req.Tools)
	}

	if strings.TrimSpace(promptText) == "" {
		return nil, fmt.Errorf("no prompt text derived from messages")
	}

	// Build CLI command
	// Use stream-json to capture all turns (handles multi-turn continuations)
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}

	model := p.model
	if model != "" {
		args = append(args, "--model", model)
	}

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Disable session persistence for stateless API-like behaviour
	args = append(args, "--no-session-persistence")

	// Disable built-in CLI tools so Claude outputs structured JSON tool calls
	args = append(args, "--tools", "")

	log.Printf("[llm/claude_cli] Calling CLI: model=%s, prompt_len=%d, system_len=%d",
		model, len(promptText), len(systemPrompt))

	// Create command with context for timeout
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	cmd.Stdin = strings.NewReader(promptText)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	log.Printf("[llm/claude_cli] CLI finished in %.1fs (exit=%v)", duration.Seconds(), cmd.ProcessState.ExitCode())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("Claude CLI timed out after %v", p.timeout)
		}
		stderrText := stderr.String()
		if len(stderrText) > 500 {
			stderrText = stderrText[:500]
		}
		return nil, fmt.Errorf("Claude CLI error: %s (stderr: %s)", err, stderrText)
	}

	// Parse stream-json output
	content, usedModel, usage, err := parseStreamJSON(stdout.String(), model)
	if err != nil {
		return nil, err
	}

	// Parse any XML tool calls from the content
	toolCalls, cleanContent := parseXMLToolCalls(content)

	log.Printf("[llm/claude_cli] Result: model=%s, content_len=%d, tokens=%d, tool_calls=%d",
		usedModel, len(cleanContent), usage.TotalTokens, len(toolCalls))

	return &CompletionResponse{
		Content:      cleanContent,
		Role:         "assistant",
		FinishReason: "end_turn",
		Usage:        usage,
		ToolCalls:    toolCalls,
	}, nil
}

// buildPromptFromMessages converts messages to (system_prompt, prompt_text).
func buildPromptFromMessages(messages []Message) (string, string) {
	var systemParts []string
	var conversationParts []string

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "user":
			conversationParts = append(conversationParts, fmt.Sprintf("Human: %s", msg.Content))
		case "assistant":
			text := msg.Content
			// Include tool calls as context
			for _, tc := range msg.ToolCalls {
				text += fmt.Sprintf("\n[Tool call: %s(%s)]", tc.Function.Name, tc.Function.Arguments)
			}
			conversationParts = append(conversationParts, fmt.Sprintf("Assistant: %s", text))
		case "tool":
			conversationParts = append(conversationParts, fmt.Sprintf("Tool result (id=%s): %s", msg.ToolCallID, msg.Content))
		}
	}

	systemText := strings.Join(systemParts, "\n")

	// Single user message: use it directly without "Human:" prefix
	var prompt string
	if len(conversationParts) == 1 && strings.HasPrefix(conversationParts[0], "Human: ") {
		prompt = strings.TrimPrefix(conversationParts[0], "Human: ")
	} else {
		prompt = strings.Join(conversationParts, "\n\n")
	}

	return systemText, prompt
}

// injectToolDefinitions appends tool schemas and calling instructions to the system prompt.
// This tells the Claude CLI model exactly what tools are available and how to invoke them
// using the <function_calls><invoke> XML format that parseXMLToolCalls expects.
func injectToolDefinitions(systemPrompt string, tools []ToolDefinition) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n## Tools\n\n")
	sb.WriteString("You have access to the following tools. To call a tool, use this exact XML format:\n\n")
	sb.WriteString("<function_calls>\n<invoke name=\"tool_name\">\n<parameter name=\"param_name\">value</parameter>\n</invoke>\n</function_calls>\n\n")
	sb.WriteString("IMPORTANT: You MUST use the exact XML format above for tool calls. Do NOT use any other format.\n")
	sb.WriteString("Do NOT put tool calls inside markdown code blocks.\n")
	sb.WriteString("You may call multiple tools by including multiple <invoke> blocks within a single <function_calls> block.\n\n")

	for _, tool := range tools {
		sb.WriteString(fmt.Sprintf("### %s\n", tool.Function.Name))
		sb.WriteString(fmt.Sprintf("%s\n", tool.Function.Description))
		if tool.Function.Parameters != nil {
			paramsJSON, _ := json.MarshalIndent(tool.Function.Parameters, "", "  ")
			sb.WriteString(fmt.Sprintf("Parameters (JSON Schema):\n```json\n%s\n```\n\n", string(paramsJSON)))
		}
	}

	return sb.String()
}

// streamJSONEvent represents a single event from Claude CLI stream-json output.
type streamJSONEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Result     string         `json:"result"`
	NumTurns   int            `json:"num_turns"`
	SessionID  string         `json:"session_id"`
	IsError    bool           `json:"is_error"`
	Usage      map[string]int `json:"usage"`
	ModelUsage map[string]any `json:"modelUsage"`
}

// parseStreamJSON parses the stream-json output from Claude CLI.
func parseStreamJSON(stdout string, fallbackModel string) (string, string, Usage, error) {
	var assistantTexts []string
	usedModel := fallbackModel
	if usedModel == "" {
		usedModel = "unknown"
	}
	var usage Usage
	var resultText string
	numTurns := 0

	for _, line := range strings.Split(stdout, "\n") {
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
			// Full assistant message (emitted once per turn)
			for _, block := range event.Message.Content {
				if block.Type == "text" && block.Text != "" {
					assistantTexts = append(assistantTexts, block.Text)
				}
			}

		case "result":
			// Final summary
			resultText = event.Result
			numTurns = event.NumTurns

			if event.Usage != nil {
				usage.PromptTokens = event.Usage["input_tokens"]
				usage.CompletionTokens = event.Usage["output_tokens"]
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}

			if event.ModelUsage != nil {
				for k := range event.ModelUsage {
					usedModel = k
					break
				}
			}

			if event.IsError {
				return "", usedModel, usage, fmt.Errorf("Claude CLI error: %s", event.Result)
			}
		}
	}

	// Decide which content to use
	var content string
	if len(assistantTexts) > 0 {
		content = strings.Join(assistantTexts, "\n\n")
		if numTurns > 1 {
			log.Printf("[llm/claude_cli] Multi-turn response: %d turns, %d blocks concatenated",
				numTurns, len(assistantTexts))
		}
	} else if resultText != "" {
		content = resultText
	} else {
		content = strings.TrimSpace(stdout)
		log.Printf("[llm/claude_cli] Warning: No structured content found, using raw stdout")
	}

	return content, usedModel, usage, nil
}

// parseXMLToolCalls extracts tool calls from Claude's XML format.
// Returns the tool calls and the content with the XML removed.
func parseXMLToolCalls(content string) ([]ToolCall, string) {
	// Regex to find <function_calls>...</function_calls> blocks
	functionCallsRegex := regexp.MustCompile(`(?s)<function_calls>\s*(.*?)\s*</function_calls>`)
	// Regex to find <invoke name="...">...</invoke> blocks
	invokeRegex := regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)">\s*(.*?)\s*</invoke>`)
	// Regex to find <parameter name="...">...</parameter> blocks - use greedy match up to closing tag
	paramRegex := regexp.MustCompile(`(?s)<parameter\s+name="([^"]+)">(.*?)</parameter>`)

	var toolCalls []ToolCall

	// Find all function_calls blocks
	matches := functionCallsRegex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		innerContent := match[1]

		// Find all invoke blocks within this function_calls block
		invokeMatches := invokeRegex.FindAllStringSubmatch(innerContent, -1)
		for _, invokeMatch := range invokeMatches {
			funcName := invokeMatch[1]
			paramsContent := invokeMatch[2]

			// Parse parameters into a map
			params := make(map[string]string)
			paramMatches := paramRegex.FindAllStringSubmatch(paramsContent, -1)
			for _, paramMatch := range paramMatches {
				paramName := paramMatch[1]
				paramValue := strings.TrimSpace(paramMatch[2])
				params[paramName] = paramValue
				log.Printf("[llm/claude_cli] Parsed param: %s = %q", paramName, truncateStr(paramValue, 100))
			}

			// Convert params to JSON
			paramsJSON, _ := json.Marshal(params)
			log.Printf("[llm/claude_cli] Tool call JSON: %s", string(paramsJSON))

			toolCalls = append(toolCalls, ToolCall{
				ID:   "call_" + uuid.New().String()[:8],
				Type: "function",
				Function: FunctionCall{
					Name:      funcName,
					Arguments: string(paramsJSON),
				},
			})
		}
	}

	// Remove the function_calls blocks from content
	cleanContent := functionCallsRegex.ReplaceAllString(content, "")
	cleanContent = strings.TrimSpace(cleanContent)

	return toolCalls, cleanContent
}

// truncateStr shortens a string for logging.
func truncateStr(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// ValidateCLI checks if the Claude CLI is available and working.
func ValidateCLI(cliPath string) error {
	if cliPath == "" {
		cliPath = findClaudeCLI()
	}
	if cliPath == "" {
		return fmt.Errorf("Claude CLI not found")
	}

	// Quick health check - just verify the executable exists and is runnable
	cmd := exec.Command(cliPath, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Claude CLI validation failed: %w", err)
	}

	return nil
}
