# OfficeClaw Testing

## Overview

Standard Go `testing` package only. No external frameworks. 4 test files, 13 test functions, ~361 lines total. Tests live in `test/` directory (separate from source).

## Test Files

| File | Tests | Description |
|------|-------|-------------|
| `test/config_test.go` | 4 | Config defaults, validation, provider checks |
| `test/machine_targeting_test.go` | 1 (9 sub-cases) | `@machine` syntax parsing |
| `test/tasks_test.go` | 6 | Task registry, execution, timeout |
| `test/tools_test.go` | 3 | Tool registry, file access control, schemas |

## Patterns

### Table-Driven Tests
Used for machine targeting with named sub-cases via `t.Run()`:
```go
tests := []struct {
    name              string
    input             string
    expectedTargets   []string
    expectedRemaining string
}{...}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { ... })
}
```

### Setup
Fresh instances per test (no shared state):
```go
registry := tasks.NewRegistry()
logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
executor := tasks.NewExecutor(registry, logger)
```

### Assertions
Manual Go-style assertions only:
```go
if err != nil { t.Fatalf("...") }       // Fatal: stops test
if result != expected { t.Errorf("...") } // Error: continues
```

### No Mocking Framework
Dependency injection via constructors enables test isolation without mocks.

## Running Tests

```bash
make test            # go test ./test/... -v -count=1
make test-coverage   # Generates coverage.html
```

Duration: ~31s (timeout test dominates at ~30s).

## Coverage Gaps

**Not tested:**
- Agent orchestration loop (`agent/agent.go`)
- All LLM providers (`llm/*.go`)
- Telegram client (`telegram/client.go`)
- VPN tool, memory tools, messaging tool
- Pending message queue
- Telemetry
- Concurrency/race conditions
- Integration/end-to-end flows

**Estimated coverage:** ~15% of codebase. Core config, targeting, task registry, and tool registry are covered.
