package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/officeclaw/src/config"
)

// FileAccessTool provides read-only file access within allowed directories.
type FileAccessTool struct {
	allowedPaths  []string
	maxFileSizeMB int
}

// NewFileAccessTool creates a file access tool.
func NewFileAccessTool(cfg config.FileAccessConfig) *FileAccessTool {
	return &FileAccessTool{
		allowedPaths:  cfg.AllowedPaths,
		maxFileSizeMB: cfg.MaxFileSizeMB,
	}
}

func (t *FileAccessTool) Name() string { return "read_file" }

func (t *FileAccessTool) Description() string {
	return "Read the contents of a local file. Only files under configured allowed directories can be accessed. Returns file content as text."
}

func (t *FileAccessTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the file to read",
			},
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"read", "list", "stat"},
				"description": "Action to perform: read file content, list directory, or get file info",
			},
		},
		"required": []string{"path", "action"},
	}
}

type fileAccessArgs struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

func (t *FileAccessTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[fileAccessArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Normalize and validate path
	absPath, err := filepath.Abs(args.Path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if !t.isPathAllowed(absPath) {
		return "", fmt.Errorf("access denied: path '%s' is not under any allowed directory", absPath)
	}

	switch args.Action {
	case "read":
		return t.readFile(absPath)
	case "list":
		return t.listDir(absPath)
	case "stat":
		return t.statFile(absPath)
	default:
		return "", fmt.Errorf("unknown action: %s", args.Action)
	}
}

func (t *FileAccessTool) isPathAllowed(absPath string) bool {
	normalizedPath := filepath.Clean(absPath)
	for _, allowed := range t.allowedPaths {
		normalizedAllowed := filepath.Clean(allowed)
		if strings.HasPrefix(strings.ToLower(normalizedPath), strings.ToLower(normalizedAllowed)) {
			return true
		}
	}
	return false
}

func (t *FileAccessTool) readFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}

	maxSize := int64(t.maxFileSizeMB) * 1024 * 1024
	if info.Size() > maxSize {
		return "", fmt.Errorf("file too large: %d bytes (max %d MB)", info.Size(), t.maxFileSizeMB)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	return string(data), nil
}

func (t *FileAccessTool) listDir(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("listing directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Directory: %s\n\n", path))
	for _, entry := range entries {
		info, _ := entry.Info()
		typeStr := "FILE"
		if entry.IsDir() {
			typeStr = "DIR "
		}
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		sb.WriteString(fmt.Sprintf("[%s] %-40s %10d bytes\n", typeStr, entry.Name(), size))
	}
	return sb.String(), nil
}

func (t *FileAccessTool) statFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat failed: %w", err)
	}

	return fmt.Sprintf("Name: %s\nSize: %d bytes\nIsDir: %v\nModified: %s\nPermissions: %s",
		info.Name(), info.Size(), info.IsDir(), info.ModTime().Format("2006-01-02 15:04:05"), info.Mode().String()), nil
}
