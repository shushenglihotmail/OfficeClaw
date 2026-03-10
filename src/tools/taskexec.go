package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/whatsapp"
)

// AsyncThreshold is the timeout (seconds) above which tasks automatically run async.
const AsyncThreshold = 180

// TaskExecutionTool allows the LLM to run preconfigured tasks.
type TaskExecutionTool struct {
	executor    *tasks.Executor
	waClient    *whatsapp.Client
	lastChatJID string // Track last chat for notifications
}

// NewTaskExecutionTool creates a task execution tool.
func NewTaskExecutionTool(executor *tasks.Executor, waClient *whatsapp.Client) *TaskExecutionTool {
	return &TaskExecutionTool{
		executor: executor,
		waClient: waClient,
	}
}

// SetChatJID sets the chat JID for async completion notifications.
func (t *TaskExecutionTool) SetChatJID(chatJID string) {
	t.lastChatJID = chatJID
}

func (t *TaskExecutionTool) Name() string { return "execute_task" }

func (t *TaskExecutionTool) Description() string {
	taskList := t.executor.ListTasks()
	names := make([]string, 0, len(taskList))
	for _, tl := range taskList {
		names = append(names, fmt.Sprintf("%s (%s)", tl.Name, tl.Description))
	}
	return fmt.Sprintf("Execute a predefined task by name. ONLY the following tasks are allowed — do NOT invent task names: %s. "+
		"Match the user's request to the best task by its description.",
		strings.Join(names, "; "))
}

func (t *TaskExecutionTool) Parameters() map[string]interface{} {
	taskList := t.executor.ListTasks()
	taskDescs := make([]map[string]string, 0, len(taskList))
	for _, tl := range taskList {
		taskDescs = append(taskDescs, map[string]string{
			"name":        tl.Name,
			"description": tl.Description,
		})
	}
	taskJSON, _ := json.Marshal(taskDescs)

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_name": map[string]interface{}{
				"type":        "string",
				"description": fmt.Sprintf("Task to execute. Available: %s", string(taskJSON)),
			},
			"parameters": map[string]interface{}{
				"type":        "object",
				"description": "Optional parameters for the task",
			},
			"schedule": map[string]interface{}{
				"type":        "string",
				"description": "Cron expression to schedule task (e.g. '0 9 * * *' for 9AM daily)",
			},
			"async": map[string]interface{}{
				"type":        "boolean",
				"description": fmt.Sprintf("Run task in background. Auto-enabled for tasks with timeout > %ds. Set false to force synchronous execution.", AsyncThreshold),
			},
		},
		"required": []string{"task_name"},
	}
}

type taskExecArgs struct {
	TaskName   string                 `json:"task_name"`
	Parameters map[string]interface{} `json:"parameters"`
	Schedule   string                 `json:"schedule"`
	Async      *bool                  `json:"async"` // nil = auto-detect, true = force async, false = force sync
}

func (t *TaskExecutionTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[taskExecArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if args.Schedule != "" {
		err := t.executor.ScheduleTask(args.TaskName, args.Schedule)
		if err != nil {
			return "", fmt.Errorf("scheduling task: %w", err)
		}
		return fmt.Sprintf("Task '%s' scheduled: %s", args.TaskName, args.Schedule), nil
	}

	// Determine if we should run async
	taskDef, ok := t.executor.GetTask(args.TaskName)
	if !ok {
		return "", fmt.Errorf("unknown task: %s", args.TaskName)
	}

	runAsync := false
	if args.Async != nil {
		// Explicit async flag from LLM
		runAsync = *args.Async
	} else if taskDef.TimeoutSeconds > AsyncThreshold {
		// Auto-async for long-running tasks (> 180 seconds)
		runAsync = true
	}

	if runAsync {
		// Run asynchronously
		chatJID := t.lastChatJID
		taskID, logFile, err := t.executor.ExecuteAsync(ctx, args.TaskName, args.Parameters,
			func(result *tasks.TaskResult) {
				t.notifyComplete(chatJID, result)
			})
		if err != nil {
			return "", fmt.Errorf("starting async task: %w", err)
		}

		return fmt.Sprintf("Task '%s' started in background.\n"+
			"Task ID: %s\n"+
			"Log file: %s\n"+
			"You will be notified via WhatsApp when it completes.",
			args.TaskName, taskID, logFile), nil
	}

	// Run synchronously
	result, err := t.executor.Execute(ctx, args.TaskName, args.Parameters)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}

// notifyComplete sends a WhatsApp notification when an async task completes.
func (t *TaskExecutionTool) notifyComplete(chatJID string, result *tasks.TaskResult) {
	if t.waClient == nil || chatJID == "" {
		return
	}

	// Build summary + tail notification
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task '%s' completed.\n", result.TaskName))
	sb.WriteString(fmt.Sprintf("Status: %s\n", result.Status))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", result.Duration))

	if result.LogFile != "" {
		sb.WriteString(fmt.Sprintf("Log: %s\n", result.LogFile))
	}

	if result.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", result.Error))
	}

	// Add last 20 lines of output
	if result.Output != "" {
		tail := result.TailOutput(20)
		if tail != "" {
			lines := strings.Split(result.Output, "\n")
			sb.WriteString(fmt.Sprintf("\n--- Last %d of %d lines ---\n%s",
				min(20, len(lines)), len(lines), tail))
		}
	}

	// Send notification
	_ = t.waClient.SendMessage(context.Background(), chatJID, sb.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
