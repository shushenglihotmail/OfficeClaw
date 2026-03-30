# OfficeClaw Code Conventions

## 1. Code Style

- **Formatting:** Standard `gofmt` with tabs
- **Imports:** Grouped: stdlib, third-party, internal (separated by blank lines)
- **Line length:** ~80-100 characters
- **Package docs:** Every package starts with a `// Package {name}` docstring

```go
// Package agent implements the core AI agent orchestration loop.
package agent
```

## 2. Naming Conventions

### Packages
Short, lowercase, single-word: `agent`, `llm`, `mcp`, `memory`, `tasks`, `tools`

### Types
- CamelCase: `Client`, `Config`, `Agent`, `Provider`
- Interfaces end with `-er`: `Provider`, `Tool`
- Function types: `MessageHandler`

### Functions
- Constructors: `New()` or `NewPackageName()`
- Private: lowercase `resolveTask()`, `generateSessionID()`
- Methods reflect action: `Register()`, `Execute()`, `Complete()`

### Variables
- Exported constants: `MaxToolCallRounds`, `ProtocolVersion`
- Unexported constants: `maxMessageLen`
- Short names in tight scopes: `i`, `err`, `msg`, `ok`, `ctx`, `cfg`

## 3. Error Handling

### Wrapping
Always wrap with context using `fmt.Errorf("...: %w", err)`:
```go
return nil, fmt.Errorf("reading config file %s: %w", path, err)
```

### Logging
Log errors with bracketed package prefix before returning:
```go
a.logger.Printf("[agent] Tool error: %v", err)
```

### Graceful Degradation
Optional services log warning and set to nil:
```go
if err := memoryClient.HealthCheck(ctx); err != nil {
    logger.Printf("Memory service not reachable: %v", err)
    memoryClient = nil
}
```

## 4. Logging

- Standard library `log.Logger` injected via constructor
- Prefix format: `[package]` (e.g., `[agent]`, `[llm]`, `[telegram]`, `[mcp]`)
- Use `Printf` consistently
- Async logging for non-blocking I/O (memory writes)

## 5. Common Patterns

### Dependency Injection via Config Structs
```go
type Config struct {
    LLMClient    *llm.Client
    ToolRegistry *tools.Registry
    Logger       *log.Logger
}

func New(cfg Config) *Agent {
    return &Agent{llmClient: cfg.LLMClient, ...}
}
```

### Provider Pattern (Interface Polymorphism)
```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```
Router in `NewClient()` selects implementation based on config.

### Tool Registry Pattern
Thread-safe registry with `sync.RWMutex`. Tools register at startup, dispatched by name at runtime.

### Context Usage
- Always first parameter in async/I/O functions
- `context.WithCancel` at app start, propagated through shutdown
- All async operations respect `ctx.Done()`

### Mutex
- `RWMutex` for read-heavy data (tool registry, task registry)
- Always `defer` unlock
- `RLock` for reads, `Lock` for writes

### Agent Loop
Tool-calling loop with round limit (`MaxToolCallRounds = 20`). Exits when LLM returns text with no tool calls.

## 6. Comments

- Package-level: `// Package {name} ...` before package declaration
- Exported symbols: `// TypeName does ...` before declaration
- Inline: sparingly, for **why** not **what**
- TODOs: `// TODO: description`
- Struct fields: comment non-obvious or optional fields

## 7. Constants

Group related constants with `const ()` blocks. Named constants preferred over magic numbers:
```go
const (
    MaxToolCallRounds     = 20
    DefaultTimeoutSeconds = 180
    maxMessageLen         = 4096
)
```
