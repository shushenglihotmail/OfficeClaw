// Package tasks provides task registration, execution, scheduling, and logging.
// Tasks are preconfigured operations that can be triggered by the LLM.
// Each task has a timeout, produces structured logs, and reports results.
package tasks

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/telemetry"
)

// MonitorConfig configures live progress monitoring for async tasks.
type MonitorConfig struct {
	IntervalSeconds int
	Send            func(text string) // Callback to send progress message to user
}

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
	LogFile   string        `json:"log_file,omitempty"` // Path to task output log file
}

// RunningTask tracks an async task execution.
type RunningTask struct {
	ID        string
	TaskName  string
	StartTime time.Time
	LogFile   string
	Cancel    context.CancelFunc
}

// String returns a human-readable summary of the result.
func (r *TaskResult) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", r.TaskName))
	sb.WriteString(fmt.Sprintf("Status: %s\n", r.Status))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", r.Duration.Round(time.Millisecond)))
	if r.LogFile != "" {
		sb.WriteString(fmt.Sprintf("Log file: %s\n", r.LogFile))
	}
	if r.Output != "" {
		sb.WriteString(fmt.Sprintf("Output:\n%s\n", r.Output))
	}
	if r.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", r.Error))
	}
	return sb.String()
}

// TailOutput returns the last n lines of output.
func (r *TaskResult) TailOutput(n int) string {
	if r.Output == "" {
		return ""
	}
	lines := strings.Split(r.Output, "\n")
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:], "\n")
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
	registry     *Registry
	logger       *log.Logger
	logDirectory string
	mu           sync.Mutex
	scheduled    map[string]*scheduledTask
	running      map[string]*RunningTask // Track running async tasks
}

type scheduledTask struct {
	taskName string
	cron     string
	cancel   context.CancelFunc
}

// NewExecutor creates a task executor.
// Task logs are stored in the "logs" subdirectory under the current working directory.
func NewExecutor(registry *Registry, logger *log.Logger) *Executor {
	logDirectory := "logs"
	// Ensure log directory exists
	if err := os.MkdirAll(logDirectory, 0755); err != nil {
		logger.Printf("[task] Warning: could not create log directory %s: %v", logDirectory, err)
	}
	return &Executor{
		registry:     registry,
		logger:       logger,
		logDirectory: logDirectory,
		scheduled:    make(map[string]*scheduledTask),
		running:      make(map[string]*RunningTask),
	}
}

// GetTask returns a task definition by name.
func (e *Executor) GetTask(name string) (config.Task, bool) {
	return e.registry.Get(name)
}

// ListRunningTasks returns info about currently running async tasks.
func (e *Executor) ListRunningTasks() []*RunningTask {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*RunningTask, 0, len(e.running))
	for _, rt := range e.running {
		result = append(result, rt)
	}
	return result
}

// CancelTask cancels a running async task by name or ID.
// Returns the task name and true if found and cancelled, empty string and false otherwise.
func (e *Executor) CancelTask(nameOrID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, rt := range e.running {
		if rt.TaskName == nameOrID || rt.ID == nameOrID {
			rt.Cancel()
			taskName := rt.TaskName
			delete(e.running, id)
			return taskName, true
		}
	}
	return "", false
}

// LogDirectory returns the path to the task log directory.
func (e *Executor) LogDirectory() string {
	return e.logDirectory
}

// TaskLogFile holds information about a task log file.
type TaskLogFile struct {
	TaskName  string
	FilePath  string
	Timestamp time.Time
	Size      int64
}

// FindLogFiles finds log files for a task, optionally filtered by time range.
// If taskName is empty, returns logs for all tasks.
// If startTime/endTime are zero, no time filtering is applied.
func (e *Executor) FindLogFiles(taskName string, startTime, endTime time.Time) ([]TaskLogFile, error) {
	pattern := filepath.Join(e.logDirectory, "*.log")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("listing log files: %w", err)
	}

	var results []TaskLogFile
	for _, f := range files {
		base := filepath.Base(f)
		// Parse filename: <task-name>-<YYYYMMDD-HHMMSS>.log
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)

		// Find the timestamp part (last 15 chars: YYYYMMDD-HHMMSS)
		if len(name) < 16 {
			continue
		}
		timestampStr := name[len(name)-15:]
		taskPart := name[:len(name)-16] // -1 for the dash before timestamp

		// Filter by task name if specified
		if taskName != "" && taskPart != taskName {
			continue
		}

		// Parse timestamp
		ts, err := time.ParseInLocation("20060102-150405", timestampStr, time.Local)
		if err != nil {
			continue
		}

		// Filter by time range if specified
		if !startTime.IsZero() && ts.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && ts.After(endTime) {
			continue
		}

		// Get file size
		info, err := os.Stat(f)
		if err != nil {
			continue
		}

		results = append(results, TaskLogFile{
			TaskName:  taskPart,
			FilePath:  f,
			Timestamp: ts,
			Size:      info.Size(),
		})
	}

	// Sort by timestamp descending (most recent first)
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results, nil
}

// ListTasks returns info for all tasks (used by TaskExecutionTool).
func (e *Executor) ListTasks() []TaskInfo {
	return e.registry.List()
}

// createTaskLogFile creates a log file for task output.
func (e *Executor) createTaskLogFile(taskName string) (*os.File, string, error) {
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s.log", taskName, timestamp)
	filepath := filepath.Join(e.logDirectory, filename)

	file, err := os.Create(filepath)
	if err != nil {
		return nil, "", fmt.Errorf("creating task log file: %w", err)
	}
	return file, filepath, nil
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
		// Create task log file for streaming output
		logFile, logPath, err := e.createTaskLogFile(taskName)
		if err != nil {
			e.logger.Printf("[task] Warning: could not create log file: %v", err)
		} else {
			result.LogFile = logPath
			defer logFile.Close()
			// Write header to log file
			fmt.Fprintf(logFile, "=== Task: %s ===\n", taskName)
			fmt.Fprintf(logFile, "Started: %s\n", startTime.Format(time.RFC3339))
			fmt.Fprintf(logFile, "Command: %s\n", taskDef.Command)
			fmt.Fprintf(logFile, "Timeout: %s\n", timeout)
			fmt.Fprintf(logFile, "=== Output ===\n")
		}

		// Execute shell command with streaming output
		output, err := e.executeCommand(taskCtx, taskName, taskDef.Command, logFile)
		result.Duration = time.Since(startTime)
		result.Output = output

		// Write footer to log file
		if logFile != nil {
			fmt.Fprintf(logFile, "\n=== Completed ===\n")
			fmt.Fprintf(logFile, "Duration: %s\n", result.Duration)
			if taskCtx.Err() == context.DeadlineExceeded {
				fmt.Fprintf(logFile, "Status: TIMEOUT\n")
			} else if err != nil {
				fmt.Fprintf(logFile, "Status: ERROR - %v\n", err)
			} else {
				fmt.Fprintf(logFile, "Status: SUCCESS\n")
			}
		}

		if taskCtx.Err() == context.DeadlineExceeded {
			result.Status = "timeout"
			result.Error = fmt.Sprintf("task exceeded timeout of %s", timeout)
			e.logger.Printf("[task] Task '%s' TIMEOUT after %s (log: %s)", taskName, result.Duration, logPath)
			telemetry.RecordTaskExecution(ctx, taskName, "timeout", result.Duration.Seconds())
			return result, nil
		}

		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			e.logger.Printf("[task] Task '%s' FAILED: %v (log: %s)", taskName, err, logPath)
			telemetry.RecordTaskExecution(ctx, taskName, "error", result.Duration.Seconds())
			return result, nil
		}

		result.Status = "success"
		e.logger.Printf("[task] Task '%s' completed in %s (log: %s)", taskName, result.Duration, logPath)
	} else {
		// No command — this is an LLM-driven task (e.g., "assist", "summarize_inbox")
		result.Status = "success"
		result.Output = fmt.Sprintf("Task '%s' is an LLM-driven task: %s", taskName, taskDef.Description)
		result.Duration = time.Since(startTime)
		e.logger.Printf("[task] Task '%s' completed in %s", taskName, result.Duration)
	}

	telemetry.RecordTaskExecution(ctx, taskName, result.Status, result.Duration.Seconds())
	return result, nil
}

// ExecuteAsync runs a task asynchronously and calls onComplete when done.
// Returns the task ID and log file path immediately.
// If monitor is non-nil with IntervalSeconds > 0, periodically sends progress via monitor.Send.
func (e *Executor) ExecuteAsync(ctx context.Context, taskName string, params map[string]interface{},
	onComplete func(*TaskResult), monitor *MonitorConfig) (string, string, error) {

	taskDef, ok := e.registry.Get(taskName)
	if !ok {
		return "", "", fmt.Errorf("unknown task: %s", taskName)
	}

	// Generate task ID
	taskID := uuid.New().String()[:8]

	// Create log file
	logFile, logPath, err := e.createTaskLogFile(taskName)
	if err != nil {
		return "", "", fmt.Errorf("creating log file: %w", err)
	}

	timeout := time.Duration(taskDef.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	// Create cancellable context for async execution
	asyncCtx, cancel := context.WithTimeout(context.Background(), timeout)

	// Track running task
	e.mu.Lock()
	e.running[taskID] = &RunningTask{
		ID:        taskID,
		TaskName:  taskName,
		StartTime: time.Now(),
		LogFile:   logPath,
		Cancel:    cancel,
	}
	e.mu.Unlock()

	e.logger.Printf("[task] Starting async task '%s' (id: %s, timeout: %s, log: %s)", taskName, taskID, timeout, logPath)

	// Write header to log file
	fmt.Fprintf(logFile, "=== Task: %s (async) ===\n", taskName)
	fmt.Fprintf(logFile, "Task ID: %s\n", taskID)
	fmt.Fprintf(logFile, "Started: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(logFile, "Command: %s\n", taskDef.Command)
	fmt.Fprintf(logFile, "Timeout: %s\n", timeout)
	fmt.Fprintf(logFile, "=== Output ===\n")

	// Run in background
	go func() {
		defer logFile.Close()
		defer cancel()

		// Remove from running when done
		defer func() {
			e.mu.Lock()
			delete(e.running, taskID)
			e.mu.Unlock()
		}()

		// Start monitoring goroutine if configured
		monitoring := monitor != nil && monitor.IntervalSeconds > 0 && monitor.Send != nil
		var monitorDone chan struct{}
		if monitoring {
			monitorDone = make(chan struct{})
			go e.runMonitor(logPath, taskName, monitor, monitorDone)
		}

		startTime := time.Now()
		result := &TaskResult{
			TaskName:  taskName,
			StartedAt: startTime,
			LogFile:   logPath,
		}

		if taskDef.Command != "" {
			output, err := e.executeCommand(asyncCtx, taskName, taskDef.Command, logFile)
			result.Duration = time.Since(startTime)
			result.Output = output

			// Write footer to log file
			fmt.Fprintf(logFile, "\n=== Completed ===\n")
			fmt.Fprintf(logFile, "Duration: %s\n", result.Duration)

			if asyncCtx.Err() == context.DeadlineExceeded {
				result.Status = "timeout"
				result.Error = fmt.Sprintf("task exceeded timeout of %s", timeout)
				fmt.Fprintf(logFile, "Status: TIMEOUT\n")
				e.logger.Printf("[task] Async task '%s' (id: %s) TIMEOUT after %s", taskName, taskID, result.Duration)
				telemetry.RecordTaskExecution(ctx, taskName, "timeout", result.Duration.Seconds())
			} else if err != nil {
				result.Status = "error"
				result.Error = err.Error()
				fmt.Fprintf(logFile, "Status: ERROR - %v\n", err)
				e.logger.Printf("[task] Async task '%s' (id: %s) FAILED: %v", taskName, taskID, err)
				telemetry.RecordTaskExecution(ctx, taskName, "error", result.Duration.Seconds())
			} else {
				result.Status = "success"
				fmt.Fprintf(logFile, "Status: SUCCESS\n")
				e.logger.Printf("[task] Async task '%s' (id: %s) completed in %s", taskName, taskID, result.Duration)
				telemetry.RecordTaskExecution(ctx, taskName, result.Status, result.Duration.Seconds())
			}
		} else {
			result.Status = "success"
			result.Output = fmt.Sprintf("Task '%s' is an LLM-driven task: %s", taskName, taskDef.Description)
			result.Duration = time.Since(startTime)
		}

		// Stop monitoring before completion callback
		if monitorDone != nil {
			close(monitorDone)
		}

		// Call completion callback
		if onComplete != nil {
			onComplete(result)
		}
	}()

	return taskID, logPath, nil
}

// executeCommand runs a shell command and streams output to the log file.
func (e *Executor) executeCommand(ctx context.Context, taskName, command string, logFile *os.File) (string, error) {
	var cmd *exec.Cmd

	// Detect if command is a .ps1 script file
	// If it starts with a path to a .ps1 file, use pwsh -File
	// Otherwise use pwsh -Command
	trimmedCmd := strings.TrimSpace(command)
	if strings.HasSuffix(strings.ToLower(strings.Fields(trimmedCmd)[0]), ".ps1") ||
		strings.Contains(trimmedCmd, ".ps1 ") ||
		strings.HasPrefix(trimmedCmd, "-File") {
		// It's a script file - use pwsh -File (more reliable for scripts)
		if strings.HasPrefix(trimmedCmd, "-File") {
			// Already has -File flag
			cmd = exec.CommandContext(ctx, "pwsh", "-NoProfile", trimmedCmd)
		} else {
			// Prepend -File flag
			cmd = exec.CommandContext(ctx, "pwsh", "-NoProfile", "-File", trimmedCmd)
		}
	} else {
		// Regular command - use pwsh -Command
		cmd = exec.CommandContext(ctx, "pwsh", "-NoProfile", "-Command", command)
	}

	// Set up output streaming
	var outputBuf bytes.Buffer
	var writers []io.Writer

	// Always capture to buffer
	writers = append(writers, &outputBuf)

	// If log file provided, also write to it
	if logFile != nil {
		writers = append(writers, logFile)
	}

	multiWriter := io.MultiWriter(writers...)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	// Run the command
	err := cmd.Run()
	return outputBuf.String(), err
}

// runMonitor tails the log file and sends periodic progress updates.
// Stops when done channel is closed.
func (e *Executor) runMonitor(logPath, taskName string, monitor *MonitorConfig, done <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(monitor.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	var lastOffset int64
	startTime := time.Now()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			newContent, newOffset := e.readLogFrom(logPath, lastOffset)
			if newOffset > lastOffset && newContent != "" {
				lastOffset = newOffset
				lines := tailLines(newContent, 100)
				elapsed := time.Since(startTime).Round(time.Second)
				msg := fmt.Sprintf("Task '%s' progress (%s elapsed):\n---\n%s\n---",
					taskName, elapsed, lines)
				monitor.Send(msg)
			}
		}
	}
}

// readLogFrom reads a log file starting from the given byte offset.
// Returns the new content and the new offset.
func (e *Executor) readLogFrom(logPath string, offset int64) (string, int64) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", offset
	}
	defer f.Close()

	// Get current file size
	info, err := f.Stat()
	if err != nil || info.Size() <= offset {
		return "", offset
	}

	// Seek to last read position
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return "", offset
	}

	return string(data), offset + int64(len(data))
}

// tailLines returns the last n lines from a string.
func tailLines(s string, n int) string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
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
