// Package llm provides a multi-provider LLM client abstraction.
// Supports Anthropic (Claude), Azure OpenAI, and OpenAI.
// Design mirrors the LLMCrawl gateway pattern with provider routing
// and tool-calling support.
package llm

import (
	"context"
	"fmt"
	"log"

	"github.com/officeclaw/src/config"
)

// Message represents a chat message in the conversation.
type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // For role="tool"
	Name       string     `json:"name,omitempty"`         // Tool name for role="tool"
}

// ToolCall represents a function call request from the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolDefinition describes a tool the LLM can call (OpenAI format).
type ToolDefinition struct {
	Type     string             `json:"type"` // "function"
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition describes a function's schema.
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// CompletionRequest holds parameters for a chat completion call.
type CompletionRequest struct {
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"` // "auto", "none"
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
}

// CompletionResponse is the standardized LLM response.
type CompletionResponse struct {
	Content      string     `json:"content"`
	Role         string     `json:"role"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        Usage      `json:"usage"`
}

// Usage tracks token consumption.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// TokenProvider is a function that returns a bearer token for authentication.
type TokenProvider func(ctx context.Context) (string, error)

// Provider is the interface that LLM backends must implement.
type Provider interface {
	// Name returns the provider identifier (e.g., "anthropic", "azure", "openai").
	Name() string

	// ChatCompletion sends a chat completion request and returns the response.
	ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// Client is the unified LLM client that routes to the active provider.
type Client struct {
	provider     Provider
	cfg          config.LLMConfig
	systemPrompt string
}

// NewClient creates a new LLM client based on configuration.
func NewClient(cfg config.LLMConfig) (*Client, error) {
	var provider Provider
	var err error

	switch cfg.Provider {
	case "anthropic":
		// Use Claude CLI provider (SSO authenticated, no API key needed)
		provider, err = NewClaudeCLIProvider(cfg.Anthropic, cfg.Temperature, cfg.RequestTimeoutSeconds)
	case "azure":
		provider, err = NewAzureProvider(cfg.Azure, cfg.Temperature, cfg.RequestTimeoutSeconds)
	case "openai":
		provider, err = NewOpenAIProvider(cfg.OpenAI, cfg.Temperature, cfg.RequestTimeoutSeconds)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("initializing %s provider: %w", cfg.Provider, err)
	}

	log.Printf("[llm] Initialized provider: %s", provider.Name())

	return &Client{
		provider: provider,
		cfg:      cfg,
	}, nil
}

// SetSystemPrompt sets the system prompt prepended to all conversations.
func (c *Client) SetSystemPrompt(prompt string) {
	c.systemPrompt = prompt
}

// Complete sends a chat completion with tools and returns the response.
func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*CompletionResponse, error) {
	// Prepend system prompt if set
	allMessages := messages
	if c.systemPrompt != "" {
		allMessages = append([]Message{{Role: "system", Content: c.systemPrompt}}, messages...)
	}

	req := CompletionRequest{
		Messages:    allMessages,
		Tools:       tools,
		ToolChoice:  "auto",
		Temperature: c.cfg.Temperature,
	}

	return c.provider.ChatCompletion(ctx, req)
}

// ProviderName returns the name of the active provider.
func (c *Client) ProviderName() string {
	return c.provider.Name()
}

