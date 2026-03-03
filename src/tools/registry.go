// Package tools provides an extensible tool registry for LLM function calling.
// Tools are registered at startup and their schemas are passed to the LLM.
// When the LLM invokes a tool, the registry dispatches execution.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/telemetry"
)

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the unique tool identifier.
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// Parameters returns the JSON Schema for the tool's input parameters.
	Parameters() map[string]interface{}

	// Execute runs the tool with the given JSON arguments and returns a result string.
	Execute(ctx context.Context, arguments string) (string, error)
}

// Registry manages registered tools and dispatches invocations.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
	log.Printf("[tools] Registered tool: %s", tool.Name())
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Definitions returns all tool definitions in OpenAI function-calling format,
// ready to be passed to the LLM.
func (r *Registry) Definitions() []llm.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return defs
}

// Execute dispatches a tool call, records metrics, and returns the result.
func (r *Registry) Execute(ctx context.Context, toolCall llm.ToolCall) (string, error) {
	tool, ok := r.Get(toolCall.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", toolCall.Function.Name)
	}

	startTime := time.Now()
	result, err := tool.Execute(ctx, toolCall.Function.Arguments)
	duration := time.Since(startTime).Seconds()

	status := "success"
	if err != nil {
		status = "error"
	}
	telemetry.RecordToolCall(ctx, toolCall.Function.Name, status, duration)

	return result, err
}

// ParseArgs is a helper to unmarshal tool arguments JSON into a typed struct.
func ParseArgs[T any](arguments string) (T, error) {
	var args T
	err := json.Unmarshal([]byte(arguments), &args)
	return args, err
}
