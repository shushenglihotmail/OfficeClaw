// Package agent implements the core AI agent orchestration loop.
// It receives messages from listeners, sends them to the LLM with
// available tools, and executes tool calls in a loop until the LLM
// produces a final response.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/memory"
	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telemetry"
	"github.com/officeclaw/src/tools"
)

// MaxToolCallRounds limits tool-call loops to prevent runaway.
const MaxToolCallRounds = 20

// SystemPromptBase provides the LLM with context about its role and capabilities.
// Tool descriptions are appended dynamically at startup.
const SystemPromptBase = `You are OfficeClaw, an AI assistant running as a Windows background agent.
You monitor Telegram for trigger messages and help users accomplish tasks remotely.

CRITICAL RULES:
- NEVER tell the user a tool succeeded before you receive the tool result. Execute the tool FIRST, wait for the result, then report.
- NEVER hallucinate or fabricate tool results, IP addresses, server names, or status information.
- Always respond to the user via send_message with the chat_id from their message.
- Be concise but thorough. Users are messaging from their phone.
- If you need to read files to help, do so before responding.
- If a task fails, explain the error clearly and suggest alternatives.
- Respect file access boundaries - you can only read from allowed directories.
- For tasks requiring multiple steps, complete all steps before sending your final response.
- When asked to turn on, connect, or enable VPN, use the vpn_control tool with action "connect" — NOT execute_task.
- Do NOT call send_message in the same round as a tool that performs an action. Wait for the action result first.

When you receive a message, analyze the request, use appropriate tools, and send a helpful response.`

// Config holds agent dependencies.
type Config struct {
	LLMClient        *llm.Client
	ToolRegistry     *tools.Registry
	TaskExecutor     *tasks.Executor
	MemoryClient     *memory.Client // Optional: nil if memory service not available
	Logger           *log.Logger
	DefaultTask      string
	MaxContextTokens int     // For flush detection (default: 100000)
	FlushThreshold   float64 // Context percentage to trigger flush (default: 0.8)
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
	llmClient        *llm.Client
	toolRegistry     *tools.Registry
	taskExecutor     *tasks.Executor
	memoryClient     *memory.Client
	logger           *log.Logger
	defaultTask      string
	sessionID        string  // Current session ID for memory logging
	maxContextTokens int     // For flush detection
	flushThreshold   float64 // Context percentage to trigger flush
	messages         []llm.Message // Conversation history for flush detection
}

// New creates a new Agent.
func New(cfg Config) *Agent {
	// Build dynamic system prompt that includes all registered tool descriptions
	toolDefs := cfg.ToolRegistry.Definitions()
	var toolDescriptions strings.Builder
	toolDescriptions.WriteString("\n\nAvailable tools:\n")
	for i, td := range toolDefs {
		toolDescriptions.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, td.Function.Name, td.Function.Description))
	}

	systemPrompt := SystemPromptBase + toolDescriptions.String()
	cfg.LLMClient.SetSystemPrompt(systemPrompt)

	// Apply defaults for memory settings
	maxContextTokens := cfg.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = 100000
	}
	flushThreshold := cfg.FlushThreshold
	if flushThreshold <= 0 {
		flushThreshold = 0.8
	}

	return &Agent{
		llmClient:        cfg.LLMClient,
		toolRegistry:     cfg.ToolRegistry,
		taskExecutor:     cfg.TaskExecutor,
		memoryClient:     cfg.MemoryClient,
		logger:           cfg.Logger,
		defaultTask:      cfg.DefaultTask,
		sessionID:        generateSessionID(),
		maxContextTokens: maxContextTokens,
		flushThreshold:   flushThreshold,
		messages:         make([]llm.Message, 0),
	}
}

// generateSessionID creates a new session ID for memory logging.
// Format: oc-{timestamp}-{random6chars}
func generateSessionID() string {
	randomBytes := make([]byte, 3)
	rand.Read(randomBytes)
	return fmt.Sprintf("oc-%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(randomBytes))
}

// ClearSession resets conversation history and starts a new session.
// Called when user sends /clear command.
func (a *Agent) ClearSession() {
	a.messages = make([]llm.Message, 0)
	a.sessionID = generateSessionID()
	a.logger.Printf("[agent] Session cleared, new session ID: %s", a.sessionID)
}

// SessionID returns the current session ID.
func (a *Agent) SessionID() string {
	return a.sessionID
}

// ForceSummary triggers manual distillation to extract summary and facts.
// Called when user sends /summary command.
func (a *Agent) ForceSummary(ctx context.Context) (string, error) {
	if a.memoryClient == nil {
		return "Memory service not connected", nil
	}
	if len(a.messages) == 0 {
		return "No conversation to summarize", nil
	}

	// Inject distillation prompt
	distillPrompt := memory.GetDistillationPrompt(0.0) // 0.0 = manual trigger
	messagesWithDistill := append(a.messages, llm.Message{Role: "user", Content: distillPrompt})

	// Get tool definitions for the LLM call
	toolDefs := a.toolRegistry.Definitions()

	// Call LLM
	resp, err := a.llmClient.Complete(ctx, messagesWithDistill, toolDefs)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	// Parse and save distillation results
	summary, facts := memory.ParseDistillationResponse(resp.Content)
	if summary != "" {
		if err := a.memoryClient.WriteDaily(ctx, "system", "Session Summary: "+summary, a.sessionID); err != nil {
			a.logger.Printf("[agent] Failed to write summary to memory: %v", err)
		}
	}
	if facts != "" {
		if err := a.memoryClient.WriteMemory(ctx, facts, ""); err != nil {
			a.logger.Printf("[agent] Failed to write facts to memory: %v", err)
		}
	}

	// Strip markers and return clean response
	cleanResponse := memory.StripDistillationMarkers(resp.Content)
	if cleanResponse == "" {
		if summary != "" {
			return "Summary saved: " + summary, nil
		}
		return "Session summarized (no additional response)", nil
	}
	return cleanResponse, nil
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

	// Log user message to memory service (async)
	if a.memoryClient != nil {
		go func() {
			if err := a.memoryClient.WriteDaily(ctx, "user", userPrompt, a.sessionID); err != nil {
				a.logger.Printf("[agent] Failed to log user message to memory: %v", err)
			}
		}()
	}

	// Add user message to conversation history
	userMsg := llm.Message{Role: "user", Content: userPrompt}
	a.messages = append(a.messages, userMsg)

	// Build conversation with the user message
	messages := []llm.Message{userMsg}

	// Check for 80% context flush
	flushTriggered := false
	var flushUsage float64
	if a.memoryClient != nil {
		flushTriggered, flushUsage = memory.CheckFlushNeeded(a.messages, a.maxContextTokens, a.flushThreshold)
		if flushTriggered {
			a.logger.Printf("[agent] Context flush triggered at %.0f%% usage", flushUsage*100)
			distillPrompt := memory.GetDistillationPrompt(flushUsage)
			messages = append(messages, llm.Message{Role: "user", Content: distillPrompt})
		}
	}

	// Get tool definitions
	toolDefs := a.toolRegistry.Definitions()

	// Agent loop: send to LLM, execute tool calls, repeat
	var finalResponse string
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

			finalResponse = resp.Content

			// Handle distillation if flush was triggered
			if flushTriggered && a.memoryClient != nil {
				summary, facts := memory.ParseDistillationResponse(finalResponse)
				if summary != "" {
					go func() {
						if err := a.memoryClient.WriteDaily(ctx, "system", "Session Summary: "+summary, a.sessionID); err != nil {
							a.logger.Printf("[agent] Failed to write summary to memory: %v", err)
						}
					}()
				}
				if facts != "" {
					go func() {
						if err := a.memoryClient.WriteMemory(ctx, facts, ""); err != nil {
							a.logger.Printf("[agent] Failed to write facts to memory: %v", err)
						}
					}()
				}
				finalResponse = memory.StripDistillationMarkers(finalResponse)
			}

			// Log assistant response to memory service (async)
			if a.memoryClient != nil && finalResponse != "" {
				go func() {
					if err := a.memoryClient.WriteDaily(ctx, "assistant", finalResponse, a.sessionID); err != nil {
						a.logger.Printf("[agent] Failed to log assistant message to memory: %v", err)
					}
				}()
			}

			// Store assistant response in conversation history
			a.messages = append(a.messages, llm.Message{Role: "assistant", Content: finalResponse})

			if telemetry.GlobalMetrics != nil {
				telemetry.GlobalMetrics.MessagesProcessed.Add(ctx, 1)
			}

			// Log the response
			duration := time.Since(startTime).Seconds()
			a.logger.Printf("[agent] Message processed in %.1fs: %s",
				duration, truncate(finalResponse, 200))
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
	sb.WriteString("=== Incoming Telegram Message ===\n")
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
