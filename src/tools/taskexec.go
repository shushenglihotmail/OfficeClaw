package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telegram"
)

// AsyncThreshold is the timeout (seconds) above which tasks automatically run async.
const AsyncThreshold = 180

// TaskExecutionTool allows the LLM to run preconfigured tasks.
type TaskExecutionTool struct {
	executor   *tasks.Executor
	tgClient   *telegram.Client
	lastChatID string // Track last chat for notifications
}

// NewTaskExecutionTool creates a task execution tool.
func NewTaskExecutionTool(executor *tasks.Executor, tgClient *telegram.Client) *TaskExecutionTool {
	return &TaskExecutionTool{
		executor: executor,
		tgClient: tgClient,
	}
}

// SetChatID sets the chat ID for async completion notifications.
func (t *TaskExecutionTool) SetChatID(chatID string) {
	t.lastChatID = chatID
}

func (t *TaskExecutionTool) Name() string { return "execute_task" }

func (t *TaskExecutionTool) Description() string {
	taskList := t.executor.ListTasks()
	names := make([]string, 0, len(taskList))
	for _, tl := range taskList {
		names = append(names, fmt.Sprintf("%s (%s)", tl.Name, tl.Description))
	}
	return fmt.Sprintf("Execute or cancel a predefined task by name. ONLY the following tasks are allowed — do NOT invent task names: %s. "+
		"Match the user's request to the best task by its description. "+
		"To check task status or progress, use view_task_log instead. "+
		"To cancel a running task, use this tool with action 'cancel'.",
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
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"execute", "cancel"},
				"description": "Action to perform: 'execute' (default) runs the task, 'cancel' stops a running task by name or ID",
			},
			"task_name": map[string]interface{}{
				"type":        "string",
				"description": fmt.Sprintf("Task to execute or cancel. Available: %s", string(taskJSON)),
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
	Action     string                 `json:"action"`     // "execute" (default) or "cancel"
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

	// Handle cancel action
	if args.Action == "cancel" {
		return t.cancelTask(args.TaskName)
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
		// Check for duplicate unless explicitly allowed
		if !taskDef.AllowDuplicate {
			for _, rt := range t.executor.ListRunningTasks() {
				if rt.TaskName == args.TaskName {
					elapsed := time.Since(rt.StartTime).Round(time.Second)
					return fmt.Sprintf("Task '%s' is already running (ID: %s, started %s ago).\n"+
						"Log file: %s\n"+
						"Use view_task_log with action 'read_log' and log_file '%s' to check progress.\n"+
						"Use execute_task with action 'cancel' and task_name '%s' to stop it.",
						args.TaskName, rt.ID, elapsed, rt.LogFile, rt.LogFile, args.TaskName), nil
				}
			}
		}

		// Set up monitoring if configured
		chatID := t.lastChatID
		var monitor *tasks.MonitorConfig
		if taskDef.MonitoringIntervalSeconds > 0 && t.tgClient != nil && chatID != "" {
			monitor = &tasks.MonitorConfig{
				IntervalSeconds: taskDef.MonitoringIntervalSeconds,
				Send: func(text string) {
					_ = t.tgClient.SendMessage(context.Background(), chatID, text)
				},
			}
		}

		// Run asynchronously
		taskID, logFile, err := t.executor.ExecuteAsync(ctx, args.TaskName, args.Parameters,
			func(result *tasks.TaskResult) {
				t.notifyComplete(chatID, result)
			}, monitor)
		if err != nil {
			return "", fmt.Errorf("starting async task: %w", err)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Task '%s' started in background.\n", args.TaskName))
		sb.WriteString(fmt.Sprintf("Task ID: %s\n", taskID))
		sb.WriteString(fmt.Sprintf("Log file: %s\n", logFile))
		if monitor != nil {
			sb.WriteString(fmt.Sprintf("Live progress will be sent every %ds.\n", taskDef.MonitoringIntervalSeconds))
		}
		sb.WriteString("You will be notified via Telegram when it completes.\n")
		sb.WriteString(fmt.Sprintf("To check progress: use view_task_log with action 'read_log' and log_file '%s'.\n", logFile))
		sb.WriteString(fmt.Sprintf("To cancel: use execute_task with action 'cancel' and task_name '%s'.\n", args.TaskName))
		sb.WriteString("Do NOT call execute_task with action 'execute' again for this task while it is running.")
		return sb.String(), nil
	}

	// Run synchronously
	result, err := t.executor.Execute(ctx, args.TaskName, args.Parameters)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}

// cancelTask cancels a running async task.
func (t *TaskExecutionTool) cancelTask(taskName string) (string, error) {
	if taskName == "" {
		return "", fmt.Errorf("task_name is required for cancel action")
	}
	name, found := t.executor.CancelTask(taskName)
	if !found {
		// Check if the task exists at all
		if _, ok := t.executor.GetTask(taskName); !ok {
			return fmt.Sprintf("No task named '%s' exists.", taskName), nil
		}
		return fmt.Sprintf("Task '%s' is not currently running.", taskName), nil
	}
	return fmt.Sprintf("Task '%s' cancellation requested. The task will be stopped.", name), nil
}

// notifyComplete sends a Telegram notification when an async task completes.
func (t *TaskExecutionTool) notifyComplete(chatID string, result *tasks.TaskResult) {
	if t.tgClient == nil || chatID == "" {
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
	_ = t.tgClient.SendMessage(context.Background(), chatID, sb.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
