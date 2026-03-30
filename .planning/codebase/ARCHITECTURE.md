# Architecture

**Analysis Date:** 2026-03-30

## System Overview

OfficeClaw is a Windows-native AI agent that monitors Telegram for trigger messages, processes them through LLMs with tool-calling, and executes tasks autonomously. It runs as a 24/7 desktop app with a system tray interface.

```
                         +------------------+
                         |   Telegram API   |
                         +--------+---------+
                                  |
                           Long Polling
                                  |
                         +--------v---------+
                         |  telegram.Client  |  Message routing, access control,
                         |  (client.go:647)  |  machine targeting, mute/unmute
                         +--+-----+-----+---+
                            |     |     |
               OC: trigger  | OCC:|  OCCO:
                            |     |     |
                +-----------v-+  +v--------+  +v-----------+
                | agent.Agent  |  |ClaudeAgent| |CopilotAgent|
                | (agent.go    |  |(claude_   | |(copilot_   |
                |  386 lines)  |  | agent.go  | | agent.go   |
                |              |  | 476 lines)| | 470 lines) |
                +------+-------+  +----+------+ +----+-------+
                       |               |              |
               LLM+Tool Loop    Claude CLI       Copilot CLI
                       |          subprocess       subprocess
                +------v-------+       |              |
                |  llm.Client  |       |     MCP Server (stdio)
                |  (client.go) |       +------+-------+
                +------+-------+              |
                       |              +-------v--------+
               Provider routing       | mcp.Server     |
                       |              | (server.go)    |
          +-----+-----+-----+        +-------+--------+
          |     |     |     |                 |
       Claude  Azure OpenAI Copilot    tools.Registry
        CLI                  CLI       (shared tools)
                       |
                +------v-------+
                | tools.Registry|
                | (registry.go) |
                +------+--------+
                       |
     +---------+-------+------+--------+--------+-------+
     |         |       |      |        |        |       |
  send_msg  read_file exec_  vpn_   view_   memory_  get_
            (file     task   control task_  search/  identity
            access.go)(task  (vpn.  log    write
                      exec.  go)   (task   (memory.
                      go)          log.go) go)
```

## Pattern Overview

**Overall:** Event-driven agent with dependency injection and registry patterns.

**Key Characteristics:**
- Manual dependency injection in `src/main.go` (no DI framework)
- Registry pattern for tools and tasks (runtime registration)
- Provider pattern for LLM backends (compile-time interface, runtime selection)
- Three independent agent modes sharing a common tool registry
- Graceful degradation: each mode is optional and fails independently

## Layers

**Entry / Routing Layer:**
- Purpose: Receives Telegram messages, applies access control, routes to correct agent mode
- Location: `src/telegram/client.go`
- Contains: Bot API client, long-polling loop, message parsing, machine targeting (`@machine` syntax), mute/unmute, trigger prefix matching (longest-first to avoid OC:/OCC:/OCCO: conflicts)
- Depends on: `go-telegram-bot-api/v5`
- Used by: `src/main.go` (initialization), agent handlers (callbacks)

**Agent Layer:**
- Purpose: Orchestrates LLM interactions for each mode
- Location: `src/agent/`
- Contains: Three agent implementations (OC, OCC, OCCO), unified slash command system
- Depends on: `llm`, `tools`, `tasks`, `memory`, `telegram`, `pending`
- Used by: Telegram client (via `MessageHandler` callbacks set in `main.go`)

**LLM Abstraction Layer:**
- Purpose: Unified multi-provider LLM client with tool-calling support
- Location: `src/llm/`
- Contains: `Provider` interface, four implementations (Claude CLI, Azure, OpenAI, Copilot CLI), unified `Message`/`CompletionResponse` format, message format translation
- Depends on: `config`
- Used by: `agent.Agent` (OC: mode only)

**Tool Layer:**
- Purpose: Extensible tool registry for LLM function calling
- Location: `src/tools/`
- Contains: Registry, 8 tool implementations, `Tool` interface, generic `ParseArgs[T]()` helper
- Depends on: `tasks`, `telegram`, `memory`, `config`
- Used by: `agent.Agent` (OC: mode), `mcp.Server` (OCC:/OCCO: modes)

**Task Layer:**
- Purpose: Task registration, execution (sync/async), scheduling, logging, monitoring, cancellation
- Location: `src/tasks/executor.go` (758 lines -- largest file)
- Contains: `Registry`, `Executor`, cron scheduler, `RunningTask` tracker, `MonitorConfig`, log file management
- Depends on: `config`, `google/uuid`
- Used by: `tools/taskexec.go`, `tools/tasklog.go`

**MCP Server Layer:**
- Purpose: Exposes tools to external CLI agents (Claude/Copilot) via Model Context Protocol
- Location: `src/mcp/`
- Contains: JSON-RPC 2.0 stdio server, MCP protocol types
- Depends on: `tools`
- Used by: Claude CLI agent and Copilot CLI agent (OfficeClaw spawned as MCP subprocess)

**Memory Layer:**
- Purpose: HTTP client for external LLMCrawl memory service (conversation logging, semantic search)
- Location: `src/memory/`
- Contains: REST client (`client.go`), context flush detection and distillation parsing (`flush.go`)
- Depends on: `llm` (for `Message` type in flush detection)
- Used by: All three agents, `tools/memory.go`

**Infrastructure Layer:**
- Purpose: Cross-cutting concerns
- Location: `src/telemetry/`, `src/pending/`, `src/config/`, `src/tray/`
- Contains: Prometheus metrics + OpenTelemetry traces, persistent message queue (JSON file-backed), YAML config with env overrides, Windows system tray GUI
- Used by: All other layers

## Three Operating Modes

### OC: Mode (OfficeClaw Agent)

**Entry point:** `src/agent/agent.go` `HandleMessage()` (line 194)

**Data flow:**
1. Telegram client receives message with `OC:` prefix
2. Content parsed for task name (first word after prefix, defaults to `telegram.default_task`)
3. Machine targeting (`@machine`) checked; message dropped if not for this machine
4. Global commands (`/mute`, `/unmute`, `/ping`) handled at telegram layer
5. Slash commands (`/clear`, `/summary`, `/help`) handled in `main.go` handler closure (lines 191-214)
6. `Agent.HandleMessage()` called with structured `IncomingMessage`
7. User prompt built via `buildPrompt()` -- includes chat ID, sender, task name
8. User message logged to memory service (async goroutine)
9. Context flush check: if messages exceed 80% of `maxContextTokens`, inject distillation prompt
10. **Agent loop** (max 20 rounds, constant `MaxToolCallRounds` at line 25):
    - Send messages + tool definitions to `llm.Client.Complete()`
    - If response has no tool calls: final response, break
    - If response has tool calls: execute each via `toolRegistry.Execute()`, append results to messages, loop
11. If flush triggered: parse `[SUMMARY]`/`[FACTS]` markers, save to memory
12. Final response logged to memory, added to conversation history (`a.messages`)

**Key design detail:** The OC: agent does NOT send replies directly. The LLM must call the `send_message` tool with `chat_id` from the incoming message. This is enforced by the system prompt.

**State:** Conversation history stored in-memory on the `Agent` struct (`a.messages []llm.Message`). Cleared via `/clear` command. Session ID format: `oc-{YYYYMMDD-HHMMSS}-{6 hex chars}`.

### OCC: Mode (Claude CLI Agent)

**Entry point:** `src/agent/claude_agent.go` `HandleMessage()` (line 149)

**Data flow:**
1. Telegram client receives message with `OCC:` prefix, routes to `ClaudeAgent.HandleMessage()`
2. Slash commands (`/reset`, `/model`, `/models`, `/help`) checked first; legacy reset keyword also supported
3. Prompt built with sender and message body
4. User message logged to memory service (async)
5. `executeClaudeCLI()` spawns Claude CLI as subprocess (line 220):
   - Flags: `-p`, `--dangerously-skip-permissions`, `--output-format stream-json`, `--verbose`
   - MCP config: `--mcp-config '{"mcpServers":{"officeclaw":{"command":"<exe>","args":["mcp","serve"]}}}'`
   - Session persistence: `--resume <sessionID>` (per-chat, captured from first response)
   - Model override: `--model <name>` (per-chat, set with `/model` command)
   - Prompt via stdin, working directory: `telegram.claude_working_folder`
   - Timeout: 5 minutes default
6. Stream-JSON output parsed via `parseStreamJSONOutput()` (line 311) -- extracts text from `assistant` events and session ID from `result` event
7. Session ID saved for subsequent calls from same chat
8. Response sent via `sendReply()` -- on failure, queued in `pending.Queue`

**State:** `sessions map[string]string` (chatID -> sessionID), `chatModels map[string]string` (chatID -> model). Both protected by `sync.RWMutex`.

### OCCO: Mode (Copilot CLI Agent)

**Entry point:** `src/agent/copilot_agent.go` `HandleMessage()` (line 140)

**Mirrors OCC: mode with Copilot CLI differences:**
- Flags: `-p <prompt>`, `--output-format json`, `--allow-all`, `-s`, `--no-custom-instructions`
- MCP config via `--additional-mcp-config` (not `--mcp-config`)
- Session persistence via `--resume=<sessionID>` (note `=` syntax)
- Reasoning effort support: `--reasoning-effort <level>` (per-chat via `/effort` command)
- JSONL output parsed via `parseCopilotOutput()` (line 306) -- takes last `assistant.message` event

**Additional state:** `chatEfforts map[string]string` for per-chat reasoning effort overrides (low/medium/high/xhigh).

## Tool-Calling Mechanism

**OC: mode** uses structured tool calls via the LLM provider:
- **OpenAI/Azure:** Native `tool_calls` in API response (JSON function arguments, standard OpenAI format)
- **Claude CLI provider** (`src/llm/claude_cli.go`): Tool definitions injected into system prompt as text with XML calling instructions. Claude responds with `<function_calls><invoke name="..."><parameter name="...">value</parameter></invoke></function_calls>` XML blocks. Parsed by `parseXMLToolCalls()` (line 339). Tool call IDs generated as `call_<uuid8>`.
- **Copilot CLI provider** (`src/llm/copilot_cli.go`): Same XML injection and parsing pattern as Claude CLI.

**OCC:/OCCO: modes** use MCP for tool access:
- OfficeClaw spawns itself as MCP server subprocess (`officeclaw.exe mcp serve`)
- MCP server reads newline-delimited JSON-RPC over stdin, writes responses to stdout, logs to stderr
- The CLI agents (Claude/Copilot) discover and invoke tools via MCP `tools/list` and `tools/call`
- Tools available in MCP mode: `read_file`, `execute_task`, `view_task_log`, `vpn_control`, `get_identity`, `memory_search`, `memory_write`
- `send_message` is NOT available in MCP mode (no Telegram client in subprocess)

## Task Execution System

**Location:** `src/tasks/executor.go` (758 lines), `src/tools/taskexec.go` (247 lines), `src/tools/tasklog.go` (348 lines)

**Registry:** Tasks defined in `config.yaml` under `tasks:`, registered at startup in `tasks.Registry`. Only predefined tasks can be executed (security boundary). LLM cannot invent task names.

**Task config fields** (`src/config/config.go` line 142):
```go
type Task struct {
    Description               string // Human-readable description
    Command                   string // Shell command (empty = LLM-only task)
    TimeoutSeconds            int    // Execution timeout
    Schedule                  string // Cron expression for scheduled execution
    AllowDuplicate            bool   // Allow concurrent runs (default: false)
    MonitoringIntervalSeconds int    // If > 0, send progress every N seconds
}
```

**Synchronous execution** (`Executor.Execute()`, line 305):
1. Look up task in registry by name
2. Create log file: `logs/<taskname>-<YYYYMMDD-HHMMSS>.log`
3. Write header (task name, start time, command, timeout)
4. Execute command via `pwsh -NoProfile -Command <cmd>` (or `-File` for `.ps1` scripts)
5. Stream output to both in-memory buffer and log file via `io.MultiWriter`
6. Write footer (duration, status)
7. Return `TaskResult` with status (`success`/`error`/`timeout`), output, duration, log path

**Asynchronous execution** (`Executor.ExecuteAsync()`, line 394):
1. Generate UUID-based task ID (first 8 chars of UUID)
2. Create log file
3. Store `RunningTask` in `running map[string]*RunningTask` (includes cancel function)
4. Launch goroutine with timeout context
5. **Monitoring goroutine** (optional): if `MonitoringIntervalSeconds > 0`:
   - `runMonitor()` (line 561) tails log file at configured interval using `readLogFrom()` offset tracking
   - Sends last 100 lines of new output via `monitor.Send` callback (Telegram message)
   - Stopped when task completes via `monitorDone` channel (closed by main goroutine)
6. On completion: remove from `running` map, call `onComplete` callback (sends Telegram notification with last 20 lines)

**Duplicate prevention** (`src/tools/taskexec.go`, line 136):
- Before starting async task, iterates `ListRunningTasks()` for same `TaskName`
- If found and `AllowDuplicate` is false (default), returns info about existing run with log file path
- Prompt in system prompt reinforces: "Do NOT launch a task that is already running"

**Cancellation** (`Executor.CancelTask()`, line 190):
- Accepts task name OR task ID as argument
- Finds matching entry in `running` map
- Calls stored `Cancel()` function (context cancellation propagates to `exec.CommandContext`)
- Removes from `running` map immediately
- LLM invokes via `execute_task` tool with `action: "cancel"` and `task_name`

**Auto-async threshold** (`src/tools/taskexec.go`, line 14):
- Constant `AsyncThreshold = 180` seconds
- Tasks with `TimeoutSeconds > 180` automatically run async unless `async: false` explicitly passed

**Cron scheduling** (`Executor.StartScheduler()`, line 658):
- `StartScheduler()` called from `main.go` in a goroutine
- Iterates all tasks with `Schedule` config, calls `ScheduleTask()`
- Each scheduled task gets its own goroutine with a 1-minute ticker
- `matchesCron()` (line 702) implements simplified cron matching: `minute hour day month weekday`
- Supports `*`, ranges (`1-5`), lists (`1,3,5`), and exact values

**Command execution** (`executeCommand()`, line 515):
- All commands run via `pwsh -NoProfile -Command <cmd>`
- `.ps1` script files detected and run via `pwsh -NoProfile -File <script>`
- Output streamed to `io.MultiWriter(buffer, logFile)`

## Entry Points

**Interactive mode** (`src/main.go` `runInteractive()`, line 48):
- Triggers: Default when running `officeclaw.exe` without subcommands
- Responsibilities: Full application with Telegram listener, all three agent modes, system tray

**MCP server mode** (`src/main.go` `runMCPServer()`, line 339):
- Triggers: `officeclaw.exe mcp serve` subcommand
- Responsibilities: Stdio JSON-RPC server exposing tools to Claude/Copilot CLI
- Differences: No Telegram client, no `send_message` tool, logs to stderr not file

## Configuration & Startup

**Config loading** (`src/config/config.go` `Load()`, line 181):
1. Read YAML file (default: `config.yaml`, overrideable via `-config` flag)
2. Apply environment variable overrides: `TELEGRAM_BOT_TOKEN`, `AZURE_OPENAI_ENDPOINT`, `AZURE_OPENAI_API_KEY`, `OPENAI_API_KEY`, `CLAUDE_CLI_PATH`
3. Apply defaults (`applyDefaults()`, line 222) -- extensive defaults for all fields
4. Validate required fields (`Validate()`, line 309) -- provider-specific validation

**Initialization order** (`src/main.go` `runApp()`, line 54):
1. Load config from YAML
2. Setup file-based logging
3. Initialize telemetry (OpenTelemetry + Prometheus)
4. Initialize pending message queue (loads from `pending_messages.json`)
5. Initialize Telegram bot client (creates bot API connection)
6. Connect to Telegram (starts long-polling goroutine)
7. Drain pending messages from previous session (24h expiry)
8. Initialize LLM client (optional -- if provider empty or init fails, OC: mode disabled)
9. Initialize task registry + executor (all tasks from config registered)
10. Initialize tool registry + register all enabled tools
11. Initialize memory client (optional -- health check, graceful degradation)
12. Create OC: agent + set Telegram message handler (only if LLM client available)
13. Create OCC: Claude agent + set handler (only if Claude CLI found)
14. Create OCCO: Copilot agent + set handler (only if Copilot CLI found)
15. Start task scheduler goroutine
16. Start Telegram reconnect watchdog goroutine
17. Setup signal handler goroutine (SIGINT, SIGTERM)
18. **Run system tray** -- blocks main thread (Windows GUI threading requirement)

**Graceful shutdown sequence** (lines 294-309):
1. Tray quit or signal received -> `cancel()` called on root context
2. Stop CLI agents: `claudeAgent.Stop()` and `copilotAgent.Stop()` -- cancel running CLI processes, wait up to 30s
3. `tgClient.GracefulDisconnect(30s)`: close `shutdownCh` to reject new messages, wait for `wg` (in-flight handlers), stop polling

## Concurrency Model

**Main goroutine:** Blocked by `systray.Run()` in `src/tray/tray.go`. All other work runs in background goroutines. This is a Windows GUI threading requirement.

**Message handling goroutines:** Each incoming Telegram message spawns a goroutine in `telegram.Client.handleMessage()` (line 358: `go func() { defer c.wg.Done(); handler(...) }()`). Tracked by `sync.WaitGroup` for graceful shutdown.

**CLI agent goroutines:**
- `ClaudeAgent` and `CopilotAgent` track running CLI processes via `sync.WaitGroup`
- Each `HandleMessage` call runs the CLI synchronously within its handler goroutine
- Agent-level context (`a.ctx`) propagates cancellation to running CLI processes on `Stop()`

**Async task goroutines:** Each `ExecuteAsync()` call spawns a background goroutine. Optional monitoring goroutine runs per-task if configured.

**Memory logging:** User and assistant messages logged via fire-and-forget goroutines (`go func() { ... }()`) to avoid blocking the agent loop.

**Background goroutines started at boot:**
- Task scheduler (`go taskExecutor.StartScheduler(ctx)`)
- Telegram reconnect watchdog (`go tgClient.StartReconnectWatchdog(ctx)`)
- Signal handler (`go func() { ... }()`)
- Prometheus HTTP server (if enabled, inside `telemetry.Init()`)

**Synchronization primitives:**
- `tools.Registry`: `sync.RWMutex` protects tool map
- `tasks.Registry`: `sync.RWMutex` protects task map
- `tasks.Executor`: `sync.Mutex` protects `running` and `scheduled` maps
- `telegram.Client`: `sync.RWMutex` for handlers + muted state; `sync.Mutex` for connection state
- `ClaudeAgent` / `CopilotAgent`: `sync.RWMutex` for `sessions`, `chatModels`, `chatEfforts`
- `pending.Queue`: `sync.Mutex` for message list

**No channels for data flow.** Communication is via method calls and callbacks. Channels used only for shutdown signaling (`shutdownCh`, OS signal channel, `monitorDone`).

## Error Handling

**Strategy:** Log and continue. Errors in optional features (memory, telemetry) do not prevent operation.

**Patterns:**
- **LLM errors in OC: mode:** Logged + metrics recorded, message processing abandoned (no reply sent to user)
- **CLI errors in OCC:/OCCO:**: Error message formatted and sent back to user via Telegram
- **Tool execution errors:** Returned to LLM as `"Error: ..."` text; LLM decides how to handle/retry
- **Telegram send failures:** Message queued in `pending.Queue` (JSON file), retried on next startup, expired after 24h, max 5 retry attempts
- **Config validation errors:** Fatal at startup (`log.Fatalf`)
- **Missing optional components:** Warning logged, feature disabled, application continues

## Cross-Cutting Concerns

**Logging:** Standard library `log.Logger` with `[OfficeClaw]` prefix, file output (default: `officeclaw.log`). Subsystem tags: `[agent]`, `[claude-agent]`, `[copilot-agent]`, `[task]`, `[vpn]`, `[mcp]`, `[telegram]`, `[tools]`, `[llm/claude_cli]`, `[llm/copilot_cli]`, `[llm/azure]`, `[pending]`.

**Telemetry:** OpenTelemetry SDK with Prometheus exporter. Global singleton `telemetry.GlobalMetrics` (nil-safe -- all record functions no-op when nil). Helper functions: `RecordLLMRequest()`, `RecordToolCall()`, `RecordTaskExecution()`. Prometheus endpoint at `http://localhost:9090/metrics` when enabled.

**Validation / Security boundaries:**
- `telegram.allowed_chat_ids`: Access control for which Telegram chats can interact (empty = allow all)
- `tools.file_access.allowed_paths`: Whitelist for file read tool (case-insensitive path prefix matching)
- `tools.vpn.vpn_names`: Whitelist for VPN connections (case-insensitive)
- Task execution: Only predefined tasks from config are allowed
- Machine targeting: `@machine` syntax routes messages to specific instances

**Authentication:**
- Telegram: Bot token from @BotFather (stateless)
- Claude CLI: Pre-authenticated via organization SSO
- Copilot CLI: Pre-authenticated via GitHub OAuth
- Azure OpenAI: API key or Entra ID bearer token (`TokenProvider` callback)
- OpenAI: API key via config or `OPENAI_API_KEY` env var

---

*Architecture analysis: 2026-03-30*
