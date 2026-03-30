# Testing Patterns

**Analysis Date:** 2026-03-30

## Test Framework

**Runner:**
- Go standard library `testing` package
- No external test frameworks (no testify assertions, no gomock)

**Assertion Library:**
- Manual Go-style assertions only:
  ```go
  if err != nil { t.Fatalf("Task execution failed: %v", err) }  // Fatal: stops test
  if result != expected { t.Errorf("Expected %q, got %q", expected, result) }  // Error: continues
  ```

**Run Commands:**
```bash
make test            # go test ./test/... -v -count=1
make test-coverage   # go test ./test/... -v -coverprofile=coverage.out && go tool cover -html=coverage.out -o coverage.html
```
- `-count=1` disables test caching
- `-v` for verbose output

## Test File Organization

**Location:** Separate `test/` directory at project root (NOT co-located with source)

**Naming:** `<component>_test.go` -- e.g., `config_test.go`, `tools_test.go`, `tasks_test.go`, `machine_targeting_test.go`

**Package:** All test files use `package test` (external test package)

**Structure:**
```
test/
├── config_test.go              # Config defaults, validation
├── machine_targeting_test.go   # @machine syntax parsing
├── tasks_test.go               # Task registry, execution, timeout
├── tools_test.go               # Tool registry, file access control
└── logs/                       # Task log output from tests
```

## Test Structure

**Suite organization -- simple tests:**
```go
func TestConfigValidation(t *testing.T) {
    cfg := &config.Config{
        Telegram: config.TelegramConfig{TriggerPrefix: "OfficeClaw:", DefaultTask: "assist"},
        LLM:      config.LLMConfig{Provider: "anthropic", Anthropic: config.AnthropicConfig{Model: "claude-sonnet-4-20250514"}},
    }
    err := cfg.Validate()
    if err != nil {
        t.Errorf("Valid config should not return error: %v", err)
    }
}
```

**Table-driven tests** (used in `test/machine_targeting_test.go`):
```go
func TestParseMachineTarget(t *testing.T) {
    tests := []struct {
        name              string
        input             string
        expectedTargets   []string
        expectedRemaining string
    }{
        {name: "single target", input: "@home refactor main.go", expectedTargets: []string{"home"}, expectedRemaining: "refactor main.go"},
        {name: "no targeting", input: "hello world", expectedTargets: nil, expectedRemaining: "hello world"},
        // ... 9 sub-cases total
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            targets, remaining := telegram.ParseMachineTarget(tt.input)
            // assertions...
        })
    }
}
```

**Setup pattern:** Fresh instances per test, no shared state:
```go
func TestTaskExecution(t *testing.T) {
    registry := tasks.NewRegistry()
    registry.Register("echo_test", config.Task{
        Description:    "Echo test",
        Command:        "echo hello_world",
        TimeoutSeconds: 10,
    })
    logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
    executor := tasks.NewExecutor(registry, logger)
    // ... test body
}
```

## Mocking

**Framework:** None -- no mocking framework used

**Approach:** Dependency injection via constructors enables isolation without mocks:
- Tools receive dependencies as interfaces or concrete types in constructors
- Tests create real instances with test-specific config
- No mock LLM client exists despite CLAUDE.md mentioning "Mock LLM client available in test utilities"

**What IS mocked (by construction):**
- File access tool tested with restricted `AllowedPaths` config
- Task executor tested with simple `echo` commands
- No Telegram client needed -- tests target registry/parsing logic

## Test Types

**Unit Tests (all current tests):**
- Config validation: `test/config_test.go` -- tests struct defaults and provider validation
- Tool registry: `test/tools_test.go` -- tests registration, retrieval, definitions generation, path access control
- Task execution: `test/tasks_test.go` -- tests registry, sync execution, timeout behavior, LLM-driven tasks
- Message parsing: `test/machine_targeting_test.go` -- tests `@machine` targeting syntax

**Integration Tests:** None

**E2E Tests:** None

## Test Inventory

| File | Function | What it tests |
|------|----------|---------------|
| `test/config_test.go` | `TestConfigDefaults` | Zero-value initialization before defaults |
| `test/config_test.go` | `TestConfigValidation` | Valid anthropic config passes validation |
| `test/config_test.go` | `TestConfigValidationUnsupportedProvider` | Unknown provider fails validation |
| `test/config_test.go` | `TestConfigValidationAzureNeedsEndpoint` | Azure without endpoint fails |
| `test/tools_test.go` | `TestToolRegistry` | Register, count, get, definitions |
| `test/tools_test.go` | `TestFileAccessToolDenied` | Path outside allowed dirs is rejected |
| `test/tools_test.go` | `TestToolParameters` | Parameters returns correct JSON Schema structure |
| `test/tasks_test.go` | `TestTaskRegistryAndExecutor` | Register task, count, get by name |
| `test/tasks_test.go` | `TestTaskExecution` | Sync execution of echo command |
| `test/tasks_test.go` | `TestTaskTimeout` | Task exceeding timeout returns "timeout" status |
| `test/tasks_test.go` | `TestTaskUnknown` | Unknown task name returns error |
| `test/tasks_test.go` | `TestLLMDrivenTask` | Task without command returns success |
| `test/machine_targeting_test.go` | `TestParseMachineTarget` | 9 sub-cases for @machine parsing |

## Common Patterns

**Error testing:**
```go
_, err := faTool.Execute(context.Background(), `{"path": "C:\\secrets\\password.txt", "action": "read"}`)
if err == nil {
    t.Error("Expected access denied error for path outside allowed directories")
}
```

**Status assertion:**
```go
result, err := executor.Execute(context.Background(), "slow_task", nil)
if err != nil {
    t.Fatalf("Task execution should not error (timeout returns result): %v", err)
}
if result.Status != "timeout" {
    t.Errorf("Expected status 'timeout', got '%s'", result.Status)
}
```

**Nil vs non-nil slice assertion:**
```go
if tt.expectedTargets == nil {
    if targets != nil {
        t.Errorf("expected nil targets, got %v", targets)
    }
} else {
    if len(targets) != len(tt.expectedTargets) {
        t.Fatalf("expected %d targets, got %d: %v", len(tt.expectedTargets), len(targets), targets)
    }
}
```

## Coverage

**Requirements:** None enforced -- no minimum coverage threshold

**View coverage:**
```bash
make test-coverage   # Generates coverage.html
```

**Estimated coverage:** ~15% of codebase. Only config, task registry/execution, tool registry, and message parsing are tested.

## Coverage Gaps

**Critical -- No tests at all:**

| Area | Files | Risk | Priority |
|------|-------|------|----------|
| Agent orchestration loop | `src/agent/agent.go` | Core business logic untested | High |
| Claude CLI agent | `src/agent/claude_agent.go` | Session management, CLI invocation | High |
| Copilot CLI agent | `src/agent/copilot_agent.go` | Session management, CLI invocation | High |
| Command parsing | `src/agent/commands.go` | Slash commands, model parsing | Medium |
| All LLM providers | `src/llm/*.go` | Provider routing, response parsing | High |
| Telegram client | `src/telegram/client.go` | Message routing, trigger matching, mute/unmute | High |
| VPN tool | `src/tools/vpn.go` | VPN connect/disconnect/status | Medium |
| Memory tools | `src/tools/memory.go` | Search and write operations | Medium |
| Memory client | `src/memory/client.go` | HTTP client for memory service | Medium |
| Memory flush | `src/memory/flush.go` | Flush detection, distillation parsing | Medium |
| Messaging tool | `src/tools/messaging.go` | Telegram message sending | Low |
| Task log tool | `src/tools/tasklog.go` | Log file reading, time hint parsing | Medium |
| Pending queue | `src/pending/queue.go` | Persistence, drain, retry logic | Medium |
| MCP server | `src/mcp/server.go` | JSON-RPC protocol handling | Medium |
| Telemetry | `src/telemetry/*.go` | Metrics recording | Low |
| System tray | `src/tray/*.go` | Windows GUI (hard to test) | Low |

**New features with no test coverage:**

| Feature | Files | What needs testing |
|---------|-------|--------------------|
| `AllowDuplicate` config field | `src/config/config.go`, `src/tools/taskexec.go` | Duplicate detection logic in `Execute()` -- when `AllowDuplicate=false`, same task should be rejected if already running |
| `MonitoringIntervalSeconds` config | `src/config/config.go`, `src/tasks/executor.go` | Monitor config passed to `ExecuteAsync()`, `runMonitor()` goroutine behavior |
| `CancelTask()` method | `src/tasks/executor.go` | Cancel by name, cancel by ID, cancel nonexistent task, cancel action in `taskexec.go` |
| `runMonitor()` method | `src/tasks/executor.go` | Periodic log tailing, progress message sending, cleanup on done channel close |
| `ExecuteAsync()` | `src/tasks/executor.go` | Async execution, running task tracking, completion callback, monitoring integration |
| `readLogFrom()` / `tailLines()` | `src/tasks/executor.go` | Log file reading from offset, tail line extraction |

**Specific testable units that should be prioritized:**
1. `ParseCommand()` in `src/agent/commands.go` -- pure function, easy to table-test
2. `parseModelArgs()` in `src/agent/commands.go` -- pure function, easy to table-test
3. `FormatModelList()` in `src/agent/commands.go` -- pure function
4. `CheckFlushNeeded()` in `src/memory/flush.go` -- pure function with simple inputs
5. `ParseDistillationResponse()` in `src/memory/flush.go` -- pure function, regex parsing
6. `StripDistillationMarkers()` in `src/memory/flush.go` -- pure function
7. `CancelTask()` in `src/tasks/executor.go` -- needs running task setup but testable
8. `splitMessage()` in `src/telegram/client.go` -- pure function, easy to test
9. `parseTimeHint()` in `src/tools/tasklog.go` -- pure function with time parsing
10. `tailLines()` in `src/tasks/executor.go` -- pure function

**Race condition risks (no concurrency tests):**
- Concurrent access to tool registry
- Concurrent task execution with running task map
- Per-chat session tracking in Claude/Copilot agents
- Mute/unmute state in Telegram client

## Test Duration

Total: ~31 seconds (dominated by `TestTaskTimeout` which waits ~30s for a 1-second timeout on a `Start-Sleep -Seconds 30` command).

---

*Testing analysis: 2026-03-30*
