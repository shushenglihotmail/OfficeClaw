// Package agent implements the core AI agent orchestration loop.
// It receives messages from listeners, sends them to the LLM with
// available tools, and executes tool calls in a loop until the LLM
// produces a final response.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telemetry"
	"github.com/officeclaw/src/tools"
)

// MaxToolCallRounds limits tool-call loops to prevent runaway.
const MaxToolCallRounds = 20

// SystemPrompt provides the LLM with context about its role and capabilities.
const SystemPrompt = `You are OfficeClaw, an AI assistant running as a Windows background agent.
You monitor WhatsApp for trigger messages and help users accomplish tasks remotely.

Your capabilities:
1. send_message: Reply to WhatsApp messages. Always use this to respond to the user.
2. read_file: Read local files from allowed directories. Use this to access documents the user references.
3. execute_task: Run preconfigured tasks (scripts, scheduled operations).

Guidelines:
- Always respond to the user via send_message with the chat_id from their message.
- Be concise but thorough. Users are messaging from their phone.
- If you need to read files to help, do so before responding.
- If a task fails, explain the error clearly and suggest alternatives.
- Respect file access boundaries - you can only read from allowed directories.
- For tasks requiring multiple steps, complete all steps before sending your final response.

When you receive a message, analyze the request, use appropriate tools, and send a helpful response.`

// Config holds agent dependencies.
type Config struct {
	LLMClient    *llm.Client
	ToolRegistry *tools.Registry
	TaskExecutor *tasks.Executor
	Logger       *log.Logger
	DefaultTask  string
}

// IncomingMessage represents a trigger message from email or Teams.
type IncomingMessage struct {
	Source    string // "email" or "teams"
	SenderID  string
	Sender    string
	Subject   string
	Body      string
	ChatID    string // Teams chat/channel ID
	MessageID string // For threading replies
	Task      string // Parsed task name (may be empty for default)
}

// Agent is the core AI agent that processes incoming messages.
type Agent struct {
	llmClient    *llm.Client
	toolRegistry *tools.Registry
	taskExecutor *tasks.Executor
	logger       *log.Logger
	defaultTask  string
}

// New creates a new Agent.
func New(cfg Config) *Agent {
	// Set the system prompt so the LLM understands its role
	cfg.LLMClient.SetSystemPrompt(SystemPrompt)

	return &Agent{
		llmClient:    cfg.LLMClient,
		toolRegistry: cfg.ToolRegistry,
		taskExecutor: cfg.TaskExecutor,
		logger:       cfg.Logger,
		defaultTask:  cfg.DefaultTask,
	}
}

// HandleMessage processes an incoming trigger message through the LLM.
// This is called by email/Teams listeners when a matching message arrives.
func (a *Agent) HandleMessage(ctx context.Context, msg IncomingMessage) {
	startTime := time.Now()
	a.logger.Printf("[agent] Processing message from %s via %s (task: %s)",
		msg.Sender, msg.Source, a.resolveTask(msg.Task))

	if telemetry.GlobalMetrics != nil {
		telemetry.GlobalMetrics.MessagesReceived.Add(ctx, 1)
	}

	// Build user prompt from the incoming message
	userPrompt := a.buildPrompt(msg)

	// Build conversation with the user message
	messages := []llm.Message{
		{Role: "user", Content: userPrompt},
	}

	// Get tool definitions
	toolDefs := a.toolRegistry.Definitions()

	// Agent loop: send to LLM, execute tool calls, repeat
	for round := 0; round < MaxToolCallRounds; round++ {
		a.logger.Printf("[agent] Round %d: sending to LLM (%d messages, %d tools)",
			round+1, len(messages), len(toolDefs))

		resp, err := a.llmClient.Complete(ctx, messages, toolDefs)
		if err != nil {
			a.logger.Printf("[agent] LLM error: %v", err)
			if telemetry.GlobalMetrics != nil {
				telemetry.GlobalMetrics.MessageErrors.Add(ctx, 1)
			}
			return
		}

		// If no tool calls, we have the final response
		if len(resp.ToolCalls) == 0 {
			a.logger.Printf("[agent] Final response received (round %d, %d tokens)",
				round+1, resp.Usage.TotalTokens)

			if telemetry.GlobalMetrics != nil {
				telemetry.GlobalMetrics.MessagesProcessed.Add(ctx, 1)
			}

			// Log the response
			duration := time.Since(startTime).Seconds()
			a.logger.Printf("[agent] Message processed in %.1fs: %s",
				duration, truncate(resp.Content, 200))
			return
		}

		// Add assistant message with tool calls to conversation
		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			a.logger.Printf("[agent] Tool call: %s(%s)", tc.Function.Name, truncate(tc.Function.Arguments, 100))

			result, err := a.toolRegistry.Execute(ctx, tc)
			var resultContent string
			if err != nil {
				resultContent = fmt.Sprintf("Error: %v", err)
				a.logger.Printf("[agent] Tool error: %v", err)
			} else {
				resultContent = result
				a.logger.Printf("[agent] Tool result: %s", truncate(result, 200))
			}

			// Add tool result to conversation
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	a.logger.Printf("[agent] WARNING: hit max tool call rounds (%d)", MaxToolCallRounds)
}

// buildPrompt constructs the user prompt from the incoming message.
func (a *Agent) buildPrompt(msg IncomingMessage) string {
	task := a.resolveTask(msg.Task)

	var sb strings.Builder
	sb.WriteString("=== Incoming WhatsApp Message ===\n")
	sb.WriteString(fmt.Sprintf("From: %s\n", msg.Sender))
	sb.WriteString(fmt.Sprintf("Chat ID: %s\n", msg.ChatID))

	sb.WriteString(fmt.Sprintf("\nTask: %s\n", task))
	sb.WriteString(fmt.Sprintf("\nMessage Body:\n%s\n", msg.Body))
	sb.WriteString("\n=== End of Message ===\n")
	sb.WriteString("\nPlease process this message according to the task. ")
	sb.WriteString("Use the available tools as needed. When you need to reply, use the send_message tool ")
	sb.WriteString(fmt.Sprintf("with chat_id='%s'.", msg.ChatID))

	return sb.String()
}

// resolveTask returns the task name, falling back to default.
func (a *Agent) resolveTask(task string) string {
	if task == "" {
		return a.defaultTask
	}
	return task
}

// truncate shortens a string for logging.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// MarshalJSON is a helper for agent status reporting.
func (a *Agent) Status() map[string]interface{} {
	return map[string]interface{}{
		"llm_provider": a.llmClient.ProviderName(),
		"tools_count":  a.toolRegistry.Count(),
		"default_task": a.defaultTask,
	}
}

// StatusJSON returns agent status as JSON string.
func (a *Agent) StatusJSON() string {
	data, _ := json.MarshalIndent(a.Status(), "", "  ")
	return string(data)
}
