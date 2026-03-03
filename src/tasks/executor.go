// Package tasks provides task registration, execution, scheduling, and logging.
// Tasks are preconfigured operations that can be triggered by the LLM.
// Each task has a timeout, produces structured logs, and reports results.
package tasks

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/telemetry"
)

// TaskInfo describes a registered task (for listing).
type TaskInfo struct {
	Name        string
	Description string
	HasCommand  bool
	Schedule    string
}

// TaskResult holds the outcome of a task execution.
type TaskResult struct {
	TaskName  string        `json:"task_name"`
	Status    string        `json:"status"` // "success", "error", "timeout"
	Output    string        `json:"output"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	StartedAt time.Time     `json:"started_at"`
}

// String returns a human-readable summary of the result.
func (r *TaskResult) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", r.TaskName))
	sb.WriteString(fmt.Sprintf("Status: %s\n", r.Status))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", r.Duration.Round(time.Millisecond)))
	if r.Output != "" {
		sb.WriteString(fmt.Sprintf("Output:\n%s\n", r.Output))
	}
	if r.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", r.Error))
	}
	return sb.String()
}

// Registry manages task definitions.
type Registry struct {
	mu    sync.RWMutex
	tasks map[string]config.Task
}

// NewRegistry creates a new task registry.
func NewRegistry() *Registry {
	return &Registry{tasks: make(map[string]config.Task)}
}

// Register adds a task definition.
func (r *Registry) Register(name string, task config.Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[name] = task
}

// Get returns a task by name.
func (r *Registry) Get(name string) (config.Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[name]
	return t, ok
}

// Count returns number of registered tasks.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tasks)
}

// List returns info about all registered tasks.
func (r *Registry) List() []TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]TaskInfo, 0, len(r.tasks))
	for name, task := range r.tasks {
		infos = append(infos, TaskInfo{
			Name:        name,
			Description: task.Description,
			HasCommand:  task.Command != "",
			Schedule:    task.Schedule,
		})
	}
	return infos
}

// Executor runs tasks with timeout, logging, and metrics.
type Executor struct {
	registry  *Registry
	logger    *log.Logger
	mu        sync.Mutex
	scheduled map[string]*scheduledTask
}

type scheduledTask struct {
	taskName string
	cron     string
	cancel   context.CancelFunc
}

// NewExecutor creates a task executor.
func NewExecutor(registry *Registry, logger *log.Logger) *Executor {
	return &Executor{
		registry:  registry,
		logger:    logger,
		scheduled: make(map[string]*scheduledTask),
	}
}

// ListTasks returns info for all tasks (used by TaskExecutionTool).
func (e *Executor) ListTasks() []TaskInfo {
	return e.registry.List()
}

// Execute runs a task by name with timeout enforcement.
func (e *Executor) Execute(ctx context.Context, taskName string, params map[string]interface{}) (*TaskResult, error) {
	taskDef, ok := e.registry.Get(taskName)
	if !ok {
		return nil, fmt.Errorf("unknown task: %s", taskName)
	}

	timeout := time.Duration(taskDef.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	e.logger.Printf("[task] Starting task '%s' (timeout: %s)", taskName, timeout)
	startTime := time.Now()

	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := &TaskResult{
		TaskName:  taskName,
		StartedAt: startTime,
	}

	if taskDef.Command != "" {
		// Execute shell command
		output, err := e.executeCommand(taskCtx, taskDef.Command)
		result.Duration = time.Since(startTime)
		result.Output = output

		if taskCtx.Err() == context.DeadlineExceeded {
			result.Status = "timeout"
			result.Error = fmt.Sprintf("task exceeded timeout of %s", timeout)
			e.logger.Printf("[task] Task '%s' TIMEOUT after %s", taskName, result.Duration)
			telemetry.RecordTaskExecution(ctx, taskName, "timeout", result.Duration.Seconds())
			return result, nil
		}

		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			e.logger.Printf("[task] Task '%s' FAILED: %v", taskName, err)
			telemetry.RecordTaskExecution(ctx, taskName, "error", result.Duration.Seconds())
			return result, nil
		}

		result.Status = "success"
	} else {
		// No command — this is an LLM-driven task (e.g., "assist", "summarize_inbox")
		result.Status = "success"
		result.Output = fmt.Sprintf("Task '%s' is an LLM-driven task: %s", taskName, taskDef.Description)
		result.Duration = time.Since(startTime)
	}

	e.logger.Printf("[task] Task '%s' completed in %s", taskName, result.Duration)
	telemetry.RecordTaskExecution(ctx, taskName, result.Status, result.Duration.Seconds())
	return result, nil
}

// executeCommand runs a shell command and captures output.
func (e *Executor) executeCommand(ctx context.Context, command string) (string, error) {
	// Use PowerShell on Windows
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", command)

	output, err := cmd.CombinedOutput()
	return string(output), err
}

// ScheduleTask registers a cron-scheduled task.
func (e *Executor) ScheduleTask(taskName string, cronExpr string) error {
	_, ok := e.registry.Get(taskName)
	if !ok {
		return fmt.Errorf("unknown task: %s", taskName)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Cancel existing schedule if any
	if existing, ok := e.scheduled[taskName]; ok {
		existing.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.scheduled[taskName] = &scheduledTask{
		taskName: taskName,
		cron:     cronExpr,
		cancel:   cancel,
	}

	e.logger.Printf("[task] Scheduled task '%s' with cron: %s", taskName, cronExpr)

	// Run scheduler goroutine (simplified — checks every minute)
	go e.runScheduleLoop(ctx, taskName, cronExpr)

	return nil
}

// StartScheduler starts any tasks that have a schedule defined in config.
func (e *Executor) StartScheduler(ctx context.Context) {
	for _, info := range e.registry.List() {
		if info.Schedule != "" {
			if err := e.ScheduleTask(info.Name, info.Schedule); err != nil {
				e.logger.Printf("[task] Failed to schedule task '%s': %v", info.Name, err)
			}
		}
	}

	// Block until context is cancelled
	<-ctx.Done()

	// Cancel all scheduled tasks
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, st := range e.scheduled {
		st.cancel()
	}
}

// runScheduleLoop is a simplified cron loop that checks every minute.
func (e *Executor) runScheduleLoop(ctx context.Context, taskName, cronExpr string) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if matchesCron(cronExpr, t) {
				e.logger.Printf("[task] Cron trigger for task '%s'", taskName)
				result, err := e.Execute(ctx, taskName, nil)
				if err != nil {
					e.logger.Printf("[task] Scheduled task '%s' failed to start: %v", taskName, err)
				} else {
					e.logger.Printf("[task] Scheduled task '%s' result: %s", taskName, result.Status)
				}
			}
		}
	}
}

// matchesCron is a simplified cron matcher supporting "minute hour day month weekday".
func matchesCron(expr string, t time.Time) bool {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return false
	}

	checks := []struct {
		field string
		value int
	}{
		{parts[0], t.Minute()},
		{parts[1], t.Hour()},
		{parts[2], t.Day()},
		{parts[3], int(t.Month())},
		{parts[4], int(t.Weekday())},
	}

	for _, c := range checks {
		if !matchCronField(c.field, c.value) {
			return false
		}
	}
	return true
}

// matchCronField checks if a single cron field matches a value.
func matchCronField(field string, value int) bool {
	if field == "*" {
		return true
	}

	// Handle ranges like "1-5"
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		low, high := 0, 0
		fmt.Sscanf(parts[0], "%d", &low)
		fmt.Sscanf(parts[1], "%d", &high)
		return value >= low && value <= high
	}

	// Handle lists like "1,3,5"
	if strings.Contains(field, ",") {
		for _, p := range strings.Split(field, ",") {
			v := 0
			fmt.Sscanf(p, "%d", &v)
			if v == value {
				return true
			}
		}
		return false
	}

	// Exact match
	v := 0
	fmt.Sscanf(field, "%d", &v)
	return v == value
}
