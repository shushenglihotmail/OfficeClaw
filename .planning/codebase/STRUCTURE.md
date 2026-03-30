# Codebase Structure

**Analysis Date:** 2026-03-30

## Directory Layout

```
OfficeClaw/
├── src/                            # All Go source code (single module)
│   ├── main.go                     # Entry point, DI, MCP subcommand (432 lines)
│   ├── agent/                      # Agent orchestration (3 modes + commands)
│   │   ├── agent.go                # OC: mode - LLM + tool loop (386 lines)
│   │   ├── claude_agent.go         # OCC: mode - Claude CLI subprocess (476 lines)
│   │   ├── copilot_agent.go        # OCCO: mode - Copilot CLI subprocess (470 lines)
│   │   └── commands.go             # Slash command parsing, model lists (151 lines)
│   ├── config/                     # Configuration management
│   │   └── config.go               # YAML loading, env overrides, validation (336 lines)
│   ├── llm/                        # Multi-provider LLM abstraction
│   │   ├── client.go               # Provider interface, Client router (152 lines)
│   │   ├── claude_cli.go           # Claude CLI provider + XML tool parsing (416 lines)
│   │   ├── copilot_cli.go          # Copilot CLI provider + JSONL parsing (387 lines)
│   │   ├── azure.go                # Azure OpenAI provider (235 lines)
│   │   └── openai.go               # OpenAI API provider (150 lines)
│   ├── telegram/                   # Telegram Bot API integration
│   │   └── client.go               # Bot client, routing, machine targeting (647 lines)
│   ├── tools/                      # Extensible tool system
│   │   ├── registry.go             # Tool interface + registry + dispatch (121 lines)
│   │   ├── messaging.go            # send_message tool (68 lines)
│   │   ├── fileaccess.go           # read_file tool (145 lines)
│   │   ├── taskexec.go             # execute_task tool (247 lines)
│   │   ├── tasklog.go              # view_task_log tool (348 lines)
│   │   ├── vpn.go                  # vpn_control tool (285 lines)
│   │   ├── memory.go               # memory_search + memory_write tools (148 lines)
│   │   └── identity.go             # get_identity tool (33 lines)
│   ├── tasks/                      # Task execution & scheduling
│   │   └── executor.go             # Registry, Executor, cron, monitoring (758 lines)
│   ├── mcp/                        # Model Context Protocol server
│   │   ├── server.go               # JSON-RPC stdio server (204 lines)
│   │   └── protocol.go             # MCP + JSON-RPC type definitions (150 lines)
│   ├── memory/                     # Memory service HTTP client
│   │   ├── client.go               # REST API client (293 lines)
│   │   └── flush.go                # Context flush detection + distillation (104 lines)
│   ├── pending/                    # Persistent message queue
│   │   └── queue.go                # JSON file-backed retry queue (147 lines)
│   ├── telemetry/                  # Observability
│   │   └── telemetry.go            # OpenTelemetry + Prometheus metrics (292 lines)
│   ├── tray/                       # Windows system tray GUI
│   │   └── tray.go                 # Systray icon + menu (180 lines)
│   └── listeners/                  # Message listener interface (unused placeholder)
│       └── listeners.go            # Interface definitions only (17 lines)
├── test/                           # All test files
│   ├── config_test.go              # Config loading tests
│   ├── machine_targeting_test.go   # ParseMachineTarget tests (98 lines)
│   ├── tasks_test.go               # Task executor tests (103 lines)
│   ├── tools_test.go               # Tool registry tests (83 lines)
│   └── logs/                       # Test log fixtures
├── build/                          # Build output
│   └── officeclaw.exe              # Compiled binary
├── logs/                           # Runtime task execution logs
│   └── <taskname>-<YYYYMMDD-HHMMSS>.log
├── docs/                           # Additional documentation
├── .planning/                      # GSD planning documents
│   └── codebase/                   # Codebase analysis docs
├── .claude/                        # Claude Code configuration
├── .vscode/                        # VS Code settings
├── go.mod                          # Go module definition (github.com/officeclaw)
├── go.sum                          # Dependency checksums
├── Makefile                        # Build, test, lint, run commands
├── config.yaml                     # Runtime configuration (gitignored secrets)
├── config.example.yaml             # Configuration template
├── CLAUDE.md                       # Claude Code project instructions
├── README.md                       # User documentation
├── officeclaw.log                  # Application log (runtime)
└── pending_messages.json           # Unsent message queue (runtime, auto-created)
```

## Directory Purposes

**`src/`:**
- Purpose: All Go source code for the single `github.com/officeclaw` module
- Contains: 13 packages, 25 Go source files, ~7,750 lines of code
- Key file: `main.go` -- application entry point with all dependency wiring

**`src/agent/`:**
- Purpose: Agent orchestration for all three operating modes
- Contains: 4 files implementing OC: mode (LLM loop), OCC: mode (Claude CLI), OCCO: mode (Copilot CLI), and shared slash commands
- Key files: `agent.go` (core LLM loop), `claude_agent.go` (CLI subprocess management)

**`src/llm/`:**
- Purpose: Multi-provider LLM client with unified message format
- Contains: 5 files -- `Provider` interface, factory, and 4 provider implementations
- Key files: `client.go` (interface + router), `claude_cli.go` (XML tool call parsing)

**`src/tools/`:**
- Purpose: LLM-callable tool implementations with registry pattern
- Contains: 8 files -- 1 registry + 7 tool implementations (8 tools total, memory.go has 2)
- Key files: `registry.go` (interface + dispatch), `taskexec.go` (async execution + cancel)

**`src/tasks/`:**
- Purpose: Task definition, execution, scheduling, monitoring, and log management
- Contains: Single large file (`executor.go`, 758 lines) -- the largest file in the codebase
- Key abstractions: `Registry`, `Executor`, `RunningTask`, `MonitorConfig`, `TaskResult`

**`src/telegram/`:**
- Purpose: Telegram Bot API integration with message routing
- Contains: Single file (`client.go`, 647 lines)
- Key functions: `Connect()`, `handleMessage()`, `ParseMachineTarget()`, `GracefulDisconnect()`

**`src/mcp/`:**
- Purpose: MCP server for exposing tools to external CLI agents
- Contains: 2 files -- server implementation + protocol type definitions
- Key detail: Used when OfficeClaw spawns itself as subprocess for OCC:/OCCO: modes

**`src/memory/`:**
- Purpose: HTTP client for LLMCrawl memory service
- Contains: 2 files -- REST client + flush/distillation logic
- Key APIs: `WriteDaily()`, `Search()`, `WriteMemory()`, `CheckFlushNeeded()`

**`src/pending/`:**
- Purpose: Persistent queue for unsent Telegram messages
- Contains: 1 file -- JSON file-backed queue with retry logic
- Key behavior: Loaded from disk on startup, drained after Telegram connects, 24h expiry

**`src/config/`:**
- Purpose: YAML configuration with environment variable overrides
- Contains: 1 file with all config struct definitions and loading logic
- Key types: `Config`, `TelegramConfig`, `LLMConfig`, `ToolsConfig`, `Task`

**`src/telemetry/`:**
- Purpose: OpenTelemetry traces + Prometheus metrics
- Contains: 1 file with init, metric registration, and recording helpers
- Key pattern: Global singleton `GlobalMetrics` (nil-safe for disabled mode)

**`src/tray/`:**
- Purpose: Windows system tray icon and menu
- Contains: 1 file with programmatic ICO generation and systray setup
- Key detail: `Run()` blocks the main goroutine (Windows GUI requirement)

**`src/listeners/`:**
- Purpose: Abstract message listener interface (placeholder, not actively used)
- Contains: 1 file with interface definitions only (17 lines)

**`test/`:**
- Purpose: All test files (separate from source packages)
- Contains: 4 test files covering config, machine targeting, tasks, and tools
- Run with: `go test ./test/... -v -count=1`

**`logs/`:**
- Purpose: Runtime task execution log files
- Contains: Log files named `<taskname>-<YYYYMMDD-HHMMSS>.log`
- Generated: Yes, by `tasks.Executor.createTaskLogFile()`
- Committed: No (runtime artifacts)

**`build/`:**
- Purpose: Compiled binary output
- Contains: `officeclaw.exe`
- Generated: Yes, by `make build`
- Committed: No

## Key File Locations

**Entry Points:**
- `src/main.go:main()`: Application entry point, subcommand routing
- `src/main.go:runApp()`: Interactive mode initialization (line 54)
- `src/main.go:runMCPServer()`: MCP server mode (line 339)

**Configuration:**
- `config.yaml`: Runtime configuration (contains secrets, not committed)
- `config.example.yaml`: Configuration template
- `src/config/config.go`: Config structs, loading, validation

**Core Logic:**
- `src/agent/agent.go:HandleMessage()`: OC: mode agent loop (line 194)
- `src/agent/claude_agent.go:HandleMessage()`: OCC: mode handler (line 149)
- `src/agent/copilot_agent.go:HandleMessage()`: OCCO: mode handler (line 140)
- `src/telegram/client.go:handleMessage()`: Message routing (line 199)
- `src/tasks/executor.go:ExecuteAsync()`: Async task execution (line 394)

**Interfaces:**
- `src/tools/registry.go:Tool`: Tool interface (line 19)
- `src/llm/client.go:Provider`: LLM provider interface (line 79)
- `src/pending/queue.go:Sender`: Message sender interface (line 25)

**Testing:**
- `test/config_test.go`: Config loading and validation
- `test/machine_targeting_test.go`: ParseMachineTarget edge cases
- `test/tasks_test.go`: Task execution, cron matching
- `test/tools_test.go`: Tool registry, ParseArgs

## Naming Conventions

**Files:**
- Package main files: descriptive name matching concept (`agent.go`, `client.go`, `server.go`, `executor.go`)
- Feature files: feature name (`fileaccess.go`, `messaging.go`, `vpn.go`, `flush.go`)
- Provider implementations: `{provider}.go` or `{provider}_cli.go` (`azure.go`, `claude_cli.go`)
- Agent mode files: `{mode}_agent.go` (`claude_agent.go`, `copilot_agent.go`)
- Test files: `{area}_test.go` in `test/` directory

**Directories:**
- Lowercase, singular (`agent`, `config`, `llm`, `tools`, `memory`)
- One package per directory (standard Go convention)

## Module Boundaries

**Go module:** `github.com/officeclaw` (defined in `go.mod`)

**Package import graph:**
```
main
├── agent     (imports: llm, memory, tasks, telemetry, tools)
├── config    (no internal imports)
├── llm       (imports: config, telemetry)
├── mcp       (imports: tools)
├── memory    (imports: llm)
├── pending   (no internal imports)
├── tasks     (imports: config, telemetry)
├── telegram  (no internal imports, external: go-telegram-bot-api)
├── telemetry (imports: config)
├── tools     (imports: config, llm, memory, tasks, telegram, telemetry)
└── tray      (imports: config)
```

**Dependency direction:** `main` -> `agent` -> `llm`/`tools`/`tasks` -> `config`/`telemetry`

**Key external dependencies:**
- `github.com/go-telegram-bot-api/telegram-bot-api/v5`: Telegram Bot API
- `github.com/getlantern/systray`: Windows system tray
- `github.com/google/uuid`: UUID generation for task IDs and tool call IDs
- `go.opentelemetry.io/otel/*`: OpenTelemetry SDK (traces + metrics)
- `github.com/prometheus/client_golang`: Prometheus metrics exporter
- `gopkg.in/yaml.v3`: YAML config parsing

## Where to Add New Code

**New Tool:**
1. Create `src/tools/{toolname}.go` implementing the `Tool` interface (Name, Description, Parameters, Execute)
2. Use `ParseArgs[T]()` generic helper for JSON argument parsing
3. Add config struct to `src/config/config.go` under `ToolsConfig`
4. Register in `src/main.go` (around line 129-155, in the tool registration block)
5. Add to `config.example.yaml` under `tools:`
6. The tool is automatically available in both OC: mode (via tool registry) and OCC:/OCCO: modes (via MCP server, if registered in `runMCPServer()` too)

**New LLM Provider:**
1. Create `src/llm/{provider}.go` implementing the `Provider` interface (Name, ChatCompletion)
2. Add config struct to `src/config/config.go` under `LLMConfig`
3. Add case in `llm.NewClient()` switch (`src/llm/client.go`, line 99)
4. Add validation in `config.Validate()` (`src/config/config.go`, line 309)

**New Agent Mode:**
1. Create `src/agent/{mode}_agent.go` following the pattern of `claude_agent.go`
2. Implement `HandleMessage(ctx context.Context, msg telegram.IncomingMessage)`
3. Add trigger prefix constant and handler setter in `src/telegram/client.go`
4. Wire up handler in `src/main.go`
5. Add help text in `src/agent/commands.go` `CommandHelpText()`

**New Task:**
- Define in `config.yaml` under `tasks:` -- auto-registered at startup, no code changes needed
- For tasks with `command:`: the command runs via `pwsh` (PowerShell)
- For LLM-only tasks (no `command:`): the task name is passed to the LLM as context

**New Slash Command:**
1. Add case in `ParseCommand()` result handling (in `src/agent/commands.go` or mode-specific handler)
2. For OC: mode: handle in `src/main.go` handler closure (line 194)
3. For OCC:/OCCO: mode: handle in `handleCommand()` method of respective agent
4. Update `CommandHelpText()` in `src/agent/commands.go`

**New Configuration:**
1. Add struct field to appropriate config type in `src/config/config.go`
2. Add default in `applyDefaults()` if needed
3. Add validation in `Validate()` if needed
4. Add env override in `applyEnvOverrides()` if needed
5. Add to `config.example.yaml`

## Special Directories

**`logs/`:**
- Purpose: Task execution log files
- Generated: Yes, by `tasks.Executor`
- Committed: No
- Naming: `<taskname>-<YYYYMMDD-HHMMSS>.log`

**`build/`:**
- Purpose: Compiled binary
- Generated: Yes, by `make build`
- Committed: No

**`.planning/codebase/`:**
- Purpose: GSD codebase analysis documents
- Generated: Yes, by codebase mapping
- Committed: Yes

**`.claude/`:**
- Purpose: Claude Code tool configuration
- Generated: By Claude Code
- Committed: Yes

---

*Structure analysis: 2026-03-30*
