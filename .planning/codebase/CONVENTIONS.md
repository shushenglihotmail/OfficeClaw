# Coding Conventions

**Analysis Date:** 2026-03-30

## Naming Patterns

**Packages:**
- Lowercase, single-word names: `tools`, `agent`, `config`, `llm`, `tasks`, `telegram`, `memory`, `pending`, `mcp`, `tray`, `telemetry`
- Package comment on the primary file: `// Package tools provides an extensible tool registry for LLM function calling.`

**Types:**
- PascalCase for exported types: `Registry`, `Executor`, `TaskResult`, `RunningTask`
- Descriptive suffixes by role:
  - `*Tool` for tool implementations: `MessagingTool`, `FileAccessTool`, `VPNTool`
  - `*Config` for configuration: `TelegramConfig`, `LLMConfig`, `VPNConfig`
  - `*Client` for API clients: `Client` (in `memory`, `telegram` packages)
  - `*Agent` for agent types: `ClaudeAgent`, `CopilotAgent`
  - `*Response` for API responses: `HealthResponse`, `SearchResponse`, `WriteResponse`
- Private args structs use camelCase: `messagingArgs`, `fileAccessArgs`, `vpnArgs`, `taskExecArgs`
- Interfaces do not use `I` prefix; named by behavior: `Tool`, `Provider`, `Sender`

**Functions:**
- PascalCase for exported: `NewRegistry()`, `Execute()`, `ParseArgs()`
- camelCase for unexported: `buildPrompt()`, `resolveTask()`, `truncate()`, `matchesCron()`
- Constructor pattern: `New<Type>(deps...) *<Type>` -- e.g., `NewRegistry()`, `NewExecutor()`, `NewFileAccessTool()`
- Boolean check methods: `IsConnected()`, `IsMuted()`, `isPathAllowed()`, `isVPNAllowed()`

**Variables:**
- camelCase for locals: `taskDef`, `toolDefs`, `chatIDStr`, `outputStr`
- PascalCase for exported constants: `MaxToolCallRounds`, `AsyncThreshold`
- Common abbreviations: `ctx`, `cfg`, `err`, `msg`, `sb` (strings.Builder), `tc` (tool call)
- No hungarian notation or type prefixes

**Files:**
- Lowercase with underscores for multi-word: `claude_cli.go`, `copilot_agent.go`, `fileaccess.go`, `taskexec.go`, `tasklog.go`
- One primary type or concept per file
- Test files in separate `test/` directory (not co-located with source)

## Code Style

**Formatting:**
- Standard `gofmt` via `make fmt` -- no custom configuration
- No `.golangci.yml`, `.editorconfig`, or `.prettierrc` detected

**Linting:**
- `golangci-lint` via `make lint` -- no custom config file

**Import organization:**
1. Standard library imports
2. (blank line)
3. Third-party packages (`github.com/go-telegram-bot-api/...`, `gopkg.in/yaml.v3`)
4. Internal packages (`github.com/officeclaw/src/...`)

Example from `src/main.go`:
```go
import (
    "context"
    "flag"
    "fmt"
    "io"
    "log"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/officeclaw/src/agent"
    "github.com/officeclaw/src/config"
    "github.com/officeclaw/src/llm"
    ...
)
```

No path aliases are used.

## Error Handling

**Wrapping pattern:**
- Always wrap errors with `fmt.Errorf("context: %w", err)`:
  ```go
  return nil, fmt.Errorf("reading config file %s: %w", path, err)
  return "", fmt.Errorf("invalid arguments: %w", err)
  return "", fmt.Errorf("health check failed: %w", err)
  ```

**Multiple error collection** (used in validation in `src/config/config.go`):
```go
var errs []string
// ... append errors
if len(errs) > 0 {
    return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errs, "\n  - "))
}
return nil
```

**Return patterns:**
- Functions that can fail return `(result, error)` -- never panic
- Tool `Execute()` returns `(string, error)` -- errors become tool result text for the LLM
- Startup failures use `log.Fatalf(...)` only in `src/main.go`

**Graceful degradation for optional components:**
```go
if err := memoryClient.HealthCheck(ctx); err != nil {
    logger.Printf("Memory service not reachable at %s: %v", cfg.Tools.Memory.ServiceURL, err)
    logger.Printf("Memory features disabled")
    memoryClient = nil
}
```
This pattern is used for: memory service, LLM provider, Claude CLI, Copilot CLI.

## Logging

**Framework:** Standard library `log.Logger` -- no third-party logging

**Logger creation and injection:**
- Created in `main.go` via `setupLogging()`, then passed as constructor dependency
- Main logger prefix: `[OfficeClaw] `
- MCP server uses separate logger to stderr: `log.New(os.Stderr, "[mcp] ", log.LstdFlags|log.Lmsgprefix)`

**Component prefixes in messages:**
- `[agent]`, `[task]`, `[llm]`, `[llm/claude_cli]`, `[telegram]`, `[tools]`, `[vpn]`, `[pending]`, `[mcp]`
- Example: `logger.Printf("[agent] Processing message from %s via %s (task: %s)", ...)`

**When to log:**
- Startup: component initialization, feature availability, counts
- Message receipt and completion with timing
- Tool calls and results (truncated to 100-200 chars via `truncate()` helper)
- Errors with descriptive context
- Never log secrets or full message contents

## Configuration Pattern

**Structure** (`src/config/config.go`):
- Root `Config` struct with nested config types
- YAML tags on all fields: `yaml:"field_name"`
- Optional fields use `omitempty`: `yaml:"schedule,omitempty"`
- Boolean `Enabled` field for feature toggles on tool configs
- New task config fields: `AllowDuplicate bool` and `MonitoringIntervalSeconds int` (both with `omitempty`)

**Loading sequence** (`Config.Load()` in `src/config/config.go`):
1. Read YAML file
2. `applyEnvOverrides()` -- environment variables override specific fields
3. `applyDefaults()` -- fill zero values with sensible defaults
4. `Validate()` -- check required fields

**Defaults pattern:**
```go
if cfg.Telegram.TriggerPrefix == "" {
    cfg.Telegram.TriggerPrefix = "OC:"
}
if cfg.Tools.VPN.ConnectTimeoutSeconds <= 0 {
    cfg.Tools.VPN.ConnectTimeoutSeconds = 30
}
```

**Environment overrides** (only for critical secrets):
- `TELEGRAM_BOT_TOKEN`, `AZURE_OPENAI_API_KEY`, `AZURE_OPENAI_ENDPOINT`, `OPENAI_API_KEY`, `CLAUDE_CLI_PATH`

**Adding new config:**
1. Add field to appropriate config struct in `src/config/config.go` with `yaml` tag
2. Add default in `applyDefaults()` if needed
3. Add validation in `Validate()` if needed
4. Add to `config.example.yaml`

## Tool Implementation Pattern

**Interface** (defined in `src/tools/registry.go`):
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, arguments string) (string, error)
}
```

**Standard file structure** (e.g., `src/tools/messaging.go`):
1. Type declaration with dependencies as fields
2. Constructor: `NewMyTool(deps...) *MyTool`
3. `Name()` -- returns snake_case string: `send_message`, `read_file`, `execute_task`, `vpn_control`, `view_task_log`, `get_identity`, `memory_search`, `memory_write`
4. `Description()` -- rich, instructive text guiding the LLM on when/how to use the tool
5. `Parameters()` -- returns JSON Schema as nested `map[string]interface{}`
6. Private args struct: `type myToolArgs struct { Field string \`json:"field"\` }`
7. `Execute()` -- parses args, validates, dispatches by action

**Argument parsing:**
```go
args, err := ParseArgs[myToolArgs](arguments)
if err != nil {
    return "", fmt.Errorf("invalid arguments: %w", err)
}
```
The generic `ParseArgs[T]()` helper in `src/tools/registry.go` uses `json.Unmarshal`.

**Action dispatch pattern** (used by `fileaccess.go`, `vpn.go`, `taskexec.go`, `tasklog.go`):
```go
switch args.Action {
case "read":
    return t.readFile(absPath)
case "list":
    return t.listDir(absPath)
default:
    return "", fmt.Errorf("unknown action: %s", args.Action)
}
```

**Registration** in `src/main.go`:
```go
if cfg.Tools.MyTool.Enabled {
    toolRegistry.Register(tools.NewMyTool(cfg.Tools.MyTool))
}
```
Some tools are always registered (e.g., `IdentityTool`).

## LLM Provider Pattern

**Interface** (defined in `src/llm/client.go`):
```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```

**Client wraps provider with unified message format:**
- `CompletionRequest` / `CompletionResponse` use OpenAI-compatible format
- Providers translate to/from their native API formats
- Provider selected by `cfg.LLM.Provider` string in `NewClient()` switch

**Adding a provider:**
1. Create `src/llm/myprovider.go`
2. Implement `Provider` interface
3. Constructor: `NewMyProvider(cfg, temp, timeoutSec) (*MyProvider, error)`
4. Add case in `llm.NewClient()` switch in `src/llm/client.go`
5. Add config struct in `src/config/config.go`

## Concurrency Patterns

**Mutex usage:**
- `sync.RWMutex` for read-heavy registries: tool registry (`src/tools/registry.go`), task registry, session maps
- `sync.Mutex` for write-heavy data: running tasks map, pending queue
- Always `defer mu.Unlock()` immediately after lock

**Goroutine lifecycle:**
- `context.Context` for cancellation propagation
- `sync.WaitGroup` for tracking in-flight handlers (Telegram client, CLI agents)
- Background goroutines always check `ctx.Done()` or `done` channel in select

**Async patterns:**
- Fire-and-forget `go func()` for non-critical work: memory logging
- Tracked goroutines for critical work: message handlers (`c.wg.Add(1)` / `defer c.wg.Done()`)
- `ExecuteAsync()` in `src/tasks/executor.go` tracks running tasks in a map with cancel functions

## Dependency Injection

**Pattern:** Manual constructor injection (no DI framework)
- Components receive dependencies through config structs or constructor parameters
- `src/main.go` is the composition root
- Optional dependencies use nil checks: `if a.memoryClient != nil { ... }`

## Constants

- Package-level `const` for magic numbers: `MaxToolCallRounds = 20`, `AsyncThreshold = 180`, `maxMessageLen = 4096`
- No magic strings -- trigger prefixes and task names come from config
- Group related constants in `const ()` blocks

## Comments

**Package docs:** Required on every package (first file): `// Package {name} description.`

**Exported symbols:** GoDoc-style comment before declaration:
```go
// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
```

**Inline comments:** Sparingly, for "why" not "what":
```go
// Wait a moment for the connection to stabilize
time.Sleep(3 * time.Second)
```

**Struct field comments:** For non-obvious or optional fields:
```go
AllowDuplicate            bool   `yaml:"allow_duplicate,omitempty"`             // Allow concurrent runs (default: false)
MonitoringIntervalSeconds int    `yaml:"monitoring_interval_seconds,omitempty"` // If > 0, send progress every N seconds
```

---

*Convention analysis: 2026-03-30*
