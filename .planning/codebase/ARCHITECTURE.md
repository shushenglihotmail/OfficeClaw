# Architecture

**Analysis Date:** 2026-03-30

## Pattern Overview

**Overall:** Multi-mode AI agent orchestration system with pluggable LLM providers and extensible tool registry. Three independent agent modes operate on the same Telegram channel with graceful degradation for unavailable components.

**Key Characteristics:**
- Modular provider pattern for LLM backends (Anthropic CLI, Azure OpenAI, OpenAI, Copilot CLI)
- Registry-based tool system with unified JSON-RPC execution interface
- Three parallel agent modes with different invocation patterns (OC:/OCC:/OCCO: prefixes)
- Persistent session management with optional memory service integration
- Async task execution with cron scheduling and result notifications
- Graceful degradation when LLM providers or CLIs are unavailable

## Layers

**Message Listener Layer:**
- Purpose: Monitor Telegram for incoming messages and route to appropriate handlers
- Location: `src/telegram/`
- Contains: Bot API integration via long polling, message parsing, trigger detection
- Depends on: go-telegram-bot-api library
- Used by: Main application for message ingestion

**Agent Orchestration Layer:**
- Purpose: Core message processing loop with LLM ↔ tool-call coordination
- Location: `src/agent/`
- Contains:
  - `agent.go` - OC: mode orchestration (LLM + tool loop, max 20 rounds)
  - `claude_agent.go` - OCC: mode (Claude CLI subprocess with session persistence)
  - `copilot_agent.go` - OCCO: mode (Copilot CLI subprocess with session persistence)
  - `commands.go` - Slash command parsing and handling
- Depends on: LLM client, tool registry, task executor, memory service
- Used by: Telegram client message handlers

**LLM Provider Layer:**
- Purpose: Multi-provider abstraction with unified request/response format
- Location: `src/llm/`
- Contains:
  - `client.go` - Router and unified message format
  - `claude_cli.go` - Anthropic Claude CLI provider
  - `azure.go` - Azure OpenAI provider
  - `openai.go` - Direct OpenAI API provider
  - `copilot_cli.go` - GitHub Copilot CLI provider
- Depends on: Provider-specific SDKs and CLI executables
- Used by: Agent orchestration layer

**Tool Execution Layer:**
- Purpose: Registry pattern for LLM-callable tools, execution dispatcher, result formatting
- Location: `src/tools/`
- Contains:
  - `registry.go` - Tool registration and invocation dispatcher
  - `messaging.go` - Telegram reply tool
  - `fileaccess.go` - Local file read (whitelist-protected)
  - `taskexec.go` - Predefined task execution with async support
  - `tasklog.go` - Task execution log viewing
  - `vpn.go` - VPN management (rasdial + Entra ID)
  - `memory.go` - Memory service tools (search, write)
  - `identity.go` - Machine identity tool
- Depends on: Task executor, Telegram client, memory service
- Used by: Agent orchestration (via tool registry)

**Task Execution Layer:**
- Purpose: Task registry, subprocess execution with timeout, cron scheduling, log storage
- Location: `src/tasks/`
- Contains: Registry, executor with context cancellation, scheduler, structured result logging
- Depends on: OS exec, cron library
- Used by: Tool execution layer, main startup

**Memory & Context Management:**
- Purpose: Persistent conversation logging, semantic search, context flush detection and distillation
- Location: `src/memory/`
- Contains:
  - `client.go` - HTTP client for external memory service
  - `flush.go` - 80% context threshold detection and distillation response parsing
- Depends on: Memory service (external, gracefully degraded if unavailable)
- Used by: Agent orchestration for session logging and context management

**MCP Server Layer:**
- Purpose: Model Context Protocol implementation for exposing tools to Claude CLI
- Location: `src/mcp/`
- Contains:
  - `server.go` - JSON-RPC stdio server
  - `protocol.go` - MCP and JSON-RPC type definitions
- Depends on: Tool registry (same as main agent)
- Used by: Claude CLI agent subprocess communication

**Telemetry & Observability:**
- Purpose: OpenTelemetry tracing and Prometheus metrics
- Location: `src/telemetry/`
- Contains: OTEL trace provider init, Prometheus metric collectors
- Depends on: otel and prometheus libraries
- Used by: All layers for instrumentation

**Configuration:**
- Purpose: YAML-based configuration loading with environment variable overrides
- Location: `src/config/`
- Contains: Config struct definitions, file loader, environment variable expansion
- Depends on: gopkg.in/yaml.v3
- Used by: Main startup for all component initialization

**UI Layer:**
- Purpose: Windows system tray GUI for application lifecycle control
- Location: `src/tray/`
- Contains: System tray icon, quit menu
- Depends on: getlantern/systray
- Used by: Main thread (blocking)

## Data Flow

**OC: Mode (OfficeClaw Agent):**

1. User sends `OC: do something` to Telegram
2. Telegram listener detects prefix, parses task name (defaults to configured default)
3. Telegram client calls OfficeClaw agent handler
4. Agent builds user prompt from message context
5. Check memory for session logging
6. Check context usage for 80% flush threshold
7. Enter LLM loop (max 20 rounds):
   - Send messages + tool definitions to LLM provider
   - LLM returns text and/or tool calls
   - For each tool call:
     - Dispatch to tool registry
     - Tool executes (file read, task exec, message send, VPN, memory ops)
     - Add result to message history
   - If LLM returned text with no tool calls, exit loop
8. Parse distillation markers from final response if flush triggered
9. Log to memory service (daily logs + persistent facts)
10. Return final response to caller (sent via messaging tool or stored in pending queue)

**OCC: Mode (Claude CLI Agent):**

1. User sends `OCC: do something` to Telegram
2. Telegram listener detects trigger, routes to Claude agent handler
3. Claude agent spawns `claude.exe` subprocess with `--output-format stream-json`
4. OfficeClaw configures itself as MCP server for the session
5. Claude CLI receives:
   - User message
   - Tool definitions via MCP (same tool registry as OC: mode)
   - File access, task exec, VPN, memory, identity tools
   - MCP communication via stdin/stdout
6. Claude CLI autonomously uses tools and processes
7. Final response parsed from JSON stream
8. Response sent via Telegram (or queued to pending if send fails)
9. Session state persisted per-chat using `--resume=sessionId`

**OCCO: Mode (Copilot CLI Agent):**

1. User sends `OCCO: do something` to Telegram
2. Telegram listener detects trigger, routes to Copilot agent handler
3. Copilot agent spawns `copilot.exe` subprocess with `--allow-all` and `--output-format json`
4. OfficeClaw configures itself as MCP server via `--additional-mcp-config`
5. Copilot uses tools (same registry) with effort levels (low/medium/high/xhigh)
6. Response parsed from JSONL stream
7. Response sent via Telegram (or queued)
8. Session persistence via `--resume=sessionId`

**State Management:**

- **Conversation history:** Stored in agent instance (per-session, cleared on /clear or /reset)
- **Session ID:** Generated at agent startup (format: `oc-{timestamp}-{random}`) or retrieved from CLI agent
- **Chat-specific model override:** Per-chat setting persisted in Claude/Copilot agent instances (survives /reset)
- **Memory logging:** Asynchronous writes to memory service; distillation extracts summary+facts at 80% context
- **Pending messages:** JSON file-backed queue for unsent Telegram messages; drained on startup

## Key Abstractions

**Tool Interface:**
- Purpose: Defines contract for LLM-callable operations
- Examples: `src/tools/messaging.go`, `src/tools/taskexec.go`, `src/tools/vpn.go`
- Pattern: Each tool implements Name(), Description(), Parameters(), Execute(ctx, args) (string, error)
- Usage: Tool registry collects all implementations, generates tool definitions for LLM, dispatches Execute calls

**Provider Interface:**
- Purpose: LLM backend abstraction with unified request/response
- Examples: Claude CLI, Azure OpenAI, OpenAI, Copilot CLI
- Pattern: Each provider implements Name(), ChatCompletion(ctx, req)
- Usage: Client factory selects provider based on config, all providers return standardized CompletionResponse

**MessageHandler Callback:**
- Purpose: Loose coupling between Telegram client and agent layers
- Usage: Telegram client invokes handlers for trigger messages; OC:/OCC:/OCCO: each have dedicated handlers
- Pattern: `func(ctx context.Context, msg IncomingMessage)`

**Task Registry:**
- Purpose: Pre-registered operations that LLM can safely invoke
- Pattern: Tasks defined in YAML (task.command, task.timeout, task.schedule)
- Safety: LLM cannot invent task names; execute_task tool validates against registry

## Entry Points

**Main Application:**
- Location: `src/main.go:main()` and `src/main.go:runApp()`
- Triggers: Direct executable invocation
- Responsibilities:
  - Load config from YAML
  - Initialize logging to file
  - Initialize telemetry (OTEL + Prometheus)
  - Initialize pending message queue
  - Connect to Telegram (long polling)
  - Initialize LLM client (optional; OC: mode disabled if unavailable)
  - Initialize task executor with registry
  - Initialize tool registry with per-tool configuration
  - Create agent instances (OC: agent, Claude agent, Copilot agent)
  - Start task scheduler
  - Start Telegram reconnect watchdog
  - Block on system tray GUI (main thread requirement on Windows)
  - Graceful shutdown: cancel CLI agents, disconnect Telegram, save pending messages

**MCP Server Subcommand:**
- Location: `src/main.go:runMCPServer()`
- Triggers: `officeclaw mcp serve`
- Responsibilities:
  - Standalone MCP server for Claude CLI invocation
  - Initialize tool registry (file access, task exec, VPN, identity, memory)
  - Note: Telegram not available, so send_message tool excluded
  - Listen on stdio for MCP JSON-RPC requests
  - Execute tools via registry
  - Return results via stdout

**Telegram Message Handler:**
- Location: `src/telegram/client.go:Connect()` → long polling loop → MessageHandler callback
- Triggers: Incoming Telegram message matching trigger prefix
- Routes to:
  - Slash command handler (if message starts with /)
  - OC: handler (if machine targeted and message matches prefix)
  - OCC: handler (if Claude trigger detected)
  - OCCO: handler (if OCCO: prefix detected)

## Error Handling

**Strategy:** Graceful degradation with async retry for transient failures.

**Patterns:**

- **LLM Provider Unavailable:** If provider initialization fails at startup, OC: mode disabled; OCC:/OCCO: modes check CLI at invocation time and reply with helpful error
- **Tool Execution Failure:** Error message added to conversation; LLM receives error and can retry or report to user
- **Telegram Send Failure:** Message queued to pending_messages.json; retried on next startup with expiration (24h)
- **Memory Service Unavailable:** Memory features disabled; conversation continues without logging
- **Task Timeout:** Task killed with context cancellation; result marked as "timeout" status
- **CLI Process Crash:** Claude/Copilot agent replies with error; Telegram user notified

## Cross-Cutting Concerns

**Logging:**
- File-based (configured path, default: officeclaw.log)
- Per-module loggers with prefixes ([telegram], [agent], [tools], [mcp])
- Async writes where applicable (memory logging, task logs)

**Validation:**
- File access whitelisting in fileaccess tool
- Task name validation (only tasks in registry allowed)
- VPN name validation (only configured VPN names allowed)
- Chat ID validation (allowed_chat_ids filter)
- Path canonicalization and traversal prevention in file access tool

**Authentication:**
- Telegram: Bot token (stateless, provided by @BotFather)
- Claude CLI: Pre-authenticated via SSO (no API key needed)
- Copilot CLI: Pre-authenticated via GitHub OAuth (no API key needed)
- Azure OpenAI: API key or Entra ID bearer token
- OpenAI: API key via environment variable

**Concurrency:**
- Telegram polling: Single poller thread with sync.WaitGroup for in-flight handlers
- Tool execution: Async dispatcher in agent loop; task exec supports both sync and async
- Memory logging: Fire-and-forget goroutines to avoid blocking agent loop
- Task scheduler: Separate goroutine with sync.Mutex on task registry
- CLI agent sessions: Per-chat session tracking with RWMutex for thread-safe reads/writes

**Context & Cancellation:**
- Root context created in main, cascades to all components
- Telegram polling uses context for shutdown signal detection
- Task execution uses derived context with timeout
- CLI subprocess processes can be cancelled via Stop() method
- Graceful shutdown waits for in-flight handlers (30s timeout)

---

*Architecture analysis: 2026-03-30*
