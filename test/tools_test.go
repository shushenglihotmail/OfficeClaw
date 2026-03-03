package test

import (
	"context"
	"testing"

	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/tools"
)

func TestToolRegistry(t *testing.T) {
	registry := tools.NewRegistry()

	if registry.Count() != 0 {
		t.Errorf("Expected 0 tools, got %d", registry.Count())
	}

	// Register file access tool
	faTool := tools.NewFileAccessTool(config.FileAccessConfig{
		Enabled:       true,
		AllowedPaths:  []string{"C:\\test"},
		MaxFileSizeMB: 10,
	})
	registry.Register(faTool)

	if registry.Count() != 1 {
		t.Errorf("Expected 1 tool, got %d", registry.Count())
	}

	// Verify tool retrieval
	tool, ok := registry.Get("read_file")
	if !ok {
		t.Error("Expected to find 'read_file' tool")
	}
	if tool.Name() != "read_file" {
		t.Errorf("Expected tool name 'read_file', got '%s'", tool.Name())
	}

	// Verify definitions generated
	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Errorf("Expected 1 definition, got %d", len(defs))
	}
	if defs[0].Function.Name != "read_file" {
		t.Errorf("Expected function name 'read_file', got '%s'", defs[0].Function.Name)
	}
}

func TestFileAccessToolDenied(t *testing.T) {
	faTool := tools.NewFileAccessTool(config.FileAccessConfig{
		AllowedPaths:  []string{"C:\\allowed"},
		MaxFileSizeMB: 10,
	})

	_, err := faTool.Execute(context.Background(), `{"path": "C:\\secrets\\password.txt", "action": "read"}`)
	if err == nil {
		t.Error("Expected access denied error for path outside allowed directories")
	}
}

func TestToolParameters(t *testing.T) {
	faTool := tools.NewFileAccessTool(config.FileAccessConfig{
		AllowedPaths:  []string{"C:\\test"},
		MaxFileSizeMB: 10,
	})

	params := faTool.Parameters()
	if params["type"] != "object" {
		t.Error("Expected parameters type to be 'object'")
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected properties to be a map")
	}

	if _, ok := props["path"]; !ok {
		t.Error("Expected 'path' property in parameters")
	}
	if _, ok := props["action"]; !ok {
		t.Error("Expected 'action' property in parameters")
	}
}
