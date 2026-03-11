package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/officeclaw/src/memory"
)

// MemorySearchTool provides semantic search across conversation memories.
type MemorySearchTool struct {
	client *memory.Client
}

// NewMemorySearchTool creates a memory search tool.
func NewMemorySearchTool(client *memory.Client) *MemorySearchTool {
	return &MemorySearchTool{client: client}
}

func (t *MemorySearchTool) Name() string { return "memory_search" }

func (t *MemorySearchTool) Description() string {
	return "Search your long-term memory for relevant information from past conversations. " +
		"Use this to recall user preferences, decisions made, facts learned, and conversation history."
}

func (t *MemorySearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query to find relevant memories",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5, max: 20)",
			},
		},
		"required": []string{"query"},
	}
}

type memorySearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (t *MemorySearchTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[memorySearchArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	results, err := t.client.Search(ctx, args.Query, limit)
	if err != nil {
		return "", fmt.Errorf("memory search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant memories found for: " + args.Query, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n\n", len(results)))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- Result %d (score: %.2f) ---\n", i+1, r.Score))
		sb.WriteString(fmt.Sprintf("Source: %s\n", r.Source))
		if r.Heading != "" {
			sb.WriteString(fmt.Sprintf("Heading: %s\n", r.Heading))
		}
		sb.WriteString(fmt.Sprintf("Content:\n%s\n\n", r.Content))
	}

	return sb.String(), nil
}

// MemoryWriteTool allows saving durable facts to long-term memory.
type MemoryWriteTool struct {
	client *memory.Client
}

// NewMemoryWriteTool creates a memory write tool.
func NewMemoryWriteTool(client *memory.Client) *MemoryWriteTool {
	return &MemoryWriteTool{client: client}
}

func (t *MemoryWriteTool) Name() string { return "memory_write" }

func (t *MemoryWriteTool) Description() string {
	return "Save important information to long-term memory. " +
		"Use this to store user preferences, decisions, rules, and facts that should be remembered across sessions. " +
		"Only save durable facts that will remain true, not temporary information."
}

func (t *MemoryWriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The facts to save (use bullet points for multiple items)",
			},
			"section": map[string]interface{}{
				"type":        "string",
				"description": "Optional section header for organizing facts (e.g., 'User Preferences', 'Project Settings')",
			},
		},
		"required": []string{"content"},
	}
}

type memoryWriteArgs struct {
	Content string `json:"content"`
	Section string `json:"section"`
}

func (t *MemoryWriteTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[memoryWriteArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("content cannot be empty")
	}

	err = t.client.WriteMemory(ctx, args.Content, args.Section)
	if err != nil {
		return "", fmt.Errorf("memory write failed: %w", err)
	}

	result := "Successfully saved to long-term memory"
	if args.Section != "" {
		result += fmt.Sprintf(" under section '%s'", args.Section)
	}
	return result, nil
}
