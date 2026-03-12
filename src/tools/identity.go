package tools

import (
	"context"
	"fmt"
	"os"
)

// IdentityTool allows the LLM to discover the machine's configured name.
type IdentityTool struct {
	machineName string
}

// NewIdentityTool creates an identity tool with the given machine name.
// If machineName is empty, Execute falls back to os.Hostname().
func NewIdentityTool(machineName string) *IdentityTool {
	return &IdentityTool{machineName: machineName}
}

func (t *IdentityTool) Name() string { return "get_identity" }

func (t *IdentityTool) Description() string {
	return "Get the name/identity of this machine. Use this when asked who you are, what machine this is, or your name."
}

func (t *IdentityTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *IdentityTool) Execute(ctx context.Context, arguments string) (string, error) {
	name := t.machineName
	if name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return "", fmt.Errorf("failed to get hostname: %w", err)
		}
		name = hostname
	}
	return fmt.Sprintf("This machine is named: %s", name), nil
}
