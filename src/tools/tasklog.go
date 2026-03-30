package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/officeclaw/src/tasks"
)

// TaskLogTool allows the LLM to view task execution logs.
type TaskLogTool struct {
	executor *tasks.Executor
}

// NewTaskLogTool creates a task log viewing tool.
func NewTaskLogTool(executor *tasks.Executor) *TaskLogTool {
	return &TaskLogTool{
		executor: executor,
	}
}

func (t *TaskLogTool) Name() string { return "view_task_log" }

func (t *TaskLogTool) Description() string {
	return "View and monitor task executions. ALWAYS use this tool (not execute_task) when users ask about " +
		"task progress, status, or output. Use action 'list_running' to see currently running tasks, " +
		"'list_recent' for recent logs, or 'read_log' to read task output."
}

func (t *TaskLogTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"list_running", "list_recent", "read_log"},
				"description": "Action to perform: 'list_running' shows currently running tasks, " +
					"'list_recent' shows recent log files, 'read_log' reads a specific log file",
			},
			"task_name": map[string]interface{}{
				"type":        "string",
				"description": "Filter by task name (e.g., 'setupbuild'). Optional for list actions.",
			},
			"time_hint": map[string]interface{}{
				"type": "string",
				"description": "Approximate time when the task ran. Supports formats like: " +
					"'2:00pm', '14:00', '2:00pm today', '10:00am yesterday', '2024-03-09 14:00'. " +
					"Used to find the closest matching log file.",
			},
			"log_file": map[string]interface{}{
				"type":        "string",
				"description": "Specific log file path to read (from list_running or list_recent output)",
			},
			"lines": map[string]interface{}{
				"type":        "integer",
				"description": "Number of lines to return. Use negative for last N lines (e.g., -100 for last 100 lines). Default: all lines (max 500).",
			},
		},
		"required": []string{"action"},
	}
}

type taskLogArgs struct {
	Action   string `json:"action"`
	TaskName string `json:"task_name"`
	TimeHint string `json:"time_hint"`
	LogFile  string `json:"log_file"`
	Lines    int    `json:"lines"`
}

func (t *TaskLogTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[taskLogArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "list_running":
		return t.listRunning()
	case "list_recent":
		return t.listRecent(args.TaskName, args.TimeHint)
	case "read_log":
		return t.readLog(args.LogFile, args.TaskName, args.TimeHint, args.Lines)
	default:
		return "", fmt.Errorf("unknown action: %s", args.Action)
	}
}

// listRunning returns information about currently running tasks.
func (t *TaskLogTool) listRunning() (string, error) {
	running := t.executor.ListRunningTasks()

	if len(running) == 0 {
		return "No tasks are currently running.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Currently running tasks (%d):\n\n", len(running)))

	for _, rt := range running {
		elapsed := time.Since(rt.StartTime).Round(time.Second)
		sb.WriteString(fmt.Sprintf("- Task: %s\n", rt.TaskName))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", rt.ID))
		sb.WriteString(fmt.Sprintf("  Started: %s (%s ago)\n", rt.StartTime.Format("15:04:05"), elapsed))
		sb.WriteString(fmt.Sprintf("  Log file: %s\n\n", rt.LogFile))
	}

	return sb.String(), nil
}

// listRecent returns recent log files, optionally filtered by task name and time.
func (t *TaskLogTool) listRecent(taskName, timeHint string) (string, error) {
	startTime, endTime := parseTimeHint(timeHint)

	logs, err := t.executor.FindLogFiles(taskName, startTime, endTime)
	if err != nil {
		return "", err
	}

	if len(logs) == 0 {
		msg := "No log files found"
		if taskName != "" {
			msg += fmt.Sprintf(" for task '%s'", taskName)
		}
		if timeHint != "" {
			msg += fmt.Sprintf(" around %s", timeHint)
		}
		return msg + ".", nil
	}

	// Limit to 20 most recent
	if len(logs) > 20 {
		logs = logs[:20]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recent log files (%d):\n\n", len(logs)))

	for _, log := range logs {
		sizeStr := formatSize(log.Size)
		sb.WriteString(fmt.Sprintf("- %s\n", log.FilePath))
		sb.WriteString(fmt.Sprintf("  Task: %s | Time: %s | Size: %s\n\n",
			log.TaskName, log.Timestamp.Format("2006-01-02 15:04:05"), sizeStr))
	}

	return sb.String(), nil
}

// readLog reads the contents of a log file.
func (t *TaskLogTool) readLog(logFile, taskName, timeHint string, lines int) (string, error) {
	// If no specific file given, try to find one based on task name and time hint
	if logFile == "" {
		if taskName == "" {
			return "", fmt.Errorf("either log_file or task_name is required for read_log action")
		}

		startTime, endTime := parseTimeHint(timeHint)
		logs, err := t.executor.FindLogFiles(taskName, startTime, endTime)
		if err != nil {
			return "", err
		}

		if len(logs) == 0 {
			msg := fmt.Sprintf("No log files found for task '%s'", taskName)
			if timeHint != "" {
				msg += fmt.Sprintf(" around %s", timeHint)
			}
			return msg + ".", nil
		}

		// Use the most recent matching log
		logFile = logs[0].FilePath
	}

	// Check if this is a running task's log (for streaming indication)
	isRunning := false
	for _, rt := range t.executor.ListRunningTasks() {
		if rt.LogFile == logFile {
			isRunning = true
			break
		}
	}

	// Read the file
	content, totalLines, err := readFileLines(logFile, lines)
	if err != nil {
		return "", fmt.Errorf("reading log file: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Log file: %s ===\n", logFile))
	if isRunning {
		sb.WriteString("(Task is currently running - log may be incomplete)\n")
	}
	if lines < 0 {
		sb.WriteString(fmt.Sprintf("(Showing last %d of %d lines)\n", -lines, totalLines))
	} else if lines > 0 && totalLines > lines {
		sb.WriteString(fmt.Sprintf("(Showing first %d of %d lines)\n", lines, totalLines))
	}
	sb.WriteString("\n")
	sb.WriteString(content)

	return sb.String(), nil
}

// parseTimeHint converts a time hint string to a time range.
// Returns zero times if no hint provided or unparseable.
func parseTimeHint(hint string) (start, end time.Time) {
	if hint == "" {
		return time.Time{}, time.Time{}
	}

	hint = strings.ToLower(strings.TrimSpace(hint))
	now := time.Now()

	// Check for "today" or "yesterday" suffix
	baseDate := now
	if strings.Contains(hint, "yesterday") {
		baseDate = now.AddDate(0, 0, -1)
		hint = strings.ReplaceAll(hint, "yesterday", "")
	} else if strings.Contains(hint, "today") {
		hint = strings.ReplaceAll(hint, "today", "")
	}
	hint = strings.TrimSpace(hint)

	// Try to parse various time formats
	var parsedTime time.Time
	var err error

	// Full datetime formats
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"15:04:05",
		"15:04",
		"3:04pm",
		"3:04 pm",
		"3pm",
		"3 pm",
	}

	for _, format := range formats {
		parsedTime, err = time.ParseInLocation(format, hint, time.Local)
		if err == nil {
			break
		}
	}

	if err != nil {
		// Couldn't parse, return empty range
		return time.Time{}, time.Time{}
	}

	// If only time was parsed (year is 0), combine with base date
	if parsedTime.Year() == 0 {
		parsedTime = time.Date(
			baseDate.Year(), baseDate.Month(), baseDate.Day(),
			parsedTime.Hour(), parsedTime.Minute(), parsedTime.Second(),
			0, time.Local,
		)
	}

	// Return a 30-minute window around the parsed time
	start = parsedTime.Add(-15 * time.Minute)
	end = parsedTime.Add(15 * time.Minute)

	return start, end
}

// readFileLines reads a file and returns the requested lines.
// If lines is negative, returns the last N lines.
// If lines is positive, returns the first N lines.
// If lines is 0, returns all lines (up to 500).
func readFileLines(filepath string, lines int) (string, int, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	var allLines []string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}

	totalLines := len(allLines)

	// Determine which lines to return
	var selectedLines []string
	maxLines := 500

	if lines == 0 {
		// Return all lines (up to max)
		if len(allLines) > maxLines {
			selectedLines = allLines[:maxLines]
		} else {
			selectedLines = allLines
		}
	} else if lines < 0 {
		// Last N lines
		n := -lines
		if n > len(allLines) {
			n = len(allLines)
		}
		if n > maxLines {
			n = maxLines
		}
		selectedLines = allLines[len(allLines)-n:]
	} else {
		// First N lines
		n := lines
		if n > len(allLines) {
			n = len(allLines)
		}
		if n > maxLines {
			n = maxLines
		}
		selectedLines = allLines[:n]
	}

	return strings.Join(selectedLines, "\n"), totalLines, nil
}

// formatSize returns a human-readable file size.
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
