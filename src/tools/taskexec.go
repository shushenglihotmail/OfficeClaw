package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/officeclaw/src/tasks"
)

// TaskExecutionTool allows the LLM to run preconfigured tasks.
type TaskExecutionTool struct {
	executor *tasks.Executor
}

// NewTaskExecutionTool creates a task execution tool.
func NewTaskExecutionTool(executor *tasks.Executor) *TaskExecutionTool {
	return &TaskExecutionTool{executor: executor}
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
		},
		"required": []string{"task_name"},
	}
}

type taskExecArgs struct {
	TaskName   string                 `json:"task_name"`
	Parameters map[string]interface{} `json:"parameters"`
	Schedule   string                 `json:"schedule"`
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

	result, err := t.executor.Execute(ctx, args.TaskName, args.Parameters)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}
