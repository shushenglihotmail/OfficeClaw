# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OfficeClaw is a Windows-native AI agent system that monitors WhatsApp for trigger messages, processes them through LLMs with tool-calling capabilities, and executes tasks autonomously. The agent runs as a 24/7 desktop application with a system tray interface.

## Key Development Commands

```bash
# Build for production (no console window)
make build                  # Outputs to build/officeclaw.exe with -H windowsgui

# Build for development (with console output)
make build-console          # Outputs to build/officeclaw.exe

# Run directly in development
make run                    # Equivalent to: go run ./src

# Run MCP server (for Claude CLI integration)
./build/officeclaw.exe mcp serve

# Run tests
make test                   # Run all tests in test/
make test-coverage          # Generate coverage.html

# Code quality
make lint                   # Run golangci-lint
make fmt                    # Format code with gofmt

# Dependency management
make deps                   # Download and tidy dependencies

# Memory service utilities (requires memory service running)
make memory-health          # Check memory service status
make memory-reindex         # Rebuild vector index
make memory-context         # Get memory context
make memory-search          # Interactive semantic search
```

## Architecture Overview

OfficeClaw supports three operating modes triggered by different prefixes (all case-insensitive):

### OC: Mode (OfficeClaw Agent)
**Core Loop**: WhatsApp Listener → OfficeClaw Agent → LLM ↔ Tools (repeat until final response)

1. WhatsApp listener detects message starting with "OC:"
2. Message parsed for task name (defaults to `whatsapp.default_task`)
3. Agent builds prompt with message context
4. Agent enters LLM ↔ tool-call loop (max 20 rounds):
   - LLM returns text and/or tool calls
   - Tools execute and return results
   - Results appended to conversation
   - Repeat until LLM returns text with no tool calls

### OCC: Mode (Claude CLI Agent)
**Direct invocation**: WhatsApp Listener → Claude CLI (with auto-approval + MCP tools)

1. WhatsApp listener detects message starting with "OCC:"
2. Claude CLI spawned with `--dangerously-skip-permissions` (auto-approval)
3. OfficeClaw automatically configures itself as an MCP server for the Claude CLI session
4. Claude runs in `whatsapp.claude_working_folder` with full tool access (native + OfficeClaw tools via MCP)
5. Claude executes autonomously using both its built-in tools and OfficeClaw's tools
6. Final response sent back via WhatsApp

This mode gives Claude CLI full autonomy while still providing access to OfficeClaw tools (task execution, file access, task logs, VPN control) via MCP.
See `agent/claude_agent.go` for implementation.

### OCCO: Mode (Copilot CLI Agent)
**Direct invocation**: WhatsApp Listener → Copilot CLI (with --allow-all + MCP tools)

1. WhatsApp listener detects message starting with "OCCO:"
2. Copilot CLI spawned with `--allow-all` (auto-approval for tools, paths, URLs)
3. OfficeClaw configures itself as MCP server via `--additional-mcp-config`
4. Copilot runs in `whatsapp.copilot_working_folder` with full tool access
5. Uses `--output-format json` (JSONL) for structured response parsing
6. Session persistence via `--resume=<sessionId>` (same pattern as Claude agent)
7. Final response sent back via WhatsApp

This mode mirrors the Claude CLI agent but uses GitHub Copilot's LLM models.
See `agent/copilot_agent.go` for implementation.

### Machine-Targeted Messaging

When multiple OfficeClaw instances share the same WhatsApp account, messages can be targeted to specific machines using angle-bracket syntax after the trigger prefix:

- `OC:<home>: hello` — only the machine named "home" responds
- `OC:<home, office>: hello` — both "home" and "office" respond
- `OC: hello` — all machines respond (no filter, backward compatible)
- `OC: who are you` — each machine uses the `get_identity` tool to report its name

Machine names are configured via `whatsapp.machine_name` in config.yaml. Matching is case-insensitive. The same syntax works with all trigger prefixes (OC:, OCC:, OCCO:).

### Package Responsibilities

- **main.go**: Dependency injection, startup sequence, signal handling, MCP subcommand
- **agent/**: Core orchestration loop, prompt building, conversation management
  - `claude_agent.go`: OCC: mode - Claude CLI integration with session persistence
  - `copilot_agent.go`: OCCO: mode - Copilot CLI integration with session persistence
  - `commands.go`: Unified slash command system (parsing, model lists, help text)
- **llm/**: Multi-provider abstraction (Anthropic/Azure/OpenAI/Copilot), unified message format
- **tools/**: Registry pattern for LLM tool-calling, execution dispatcher
  - `messaging.go`: WhatsApp reply tool
  - `fileaccess.go`: Local file read tool (path-whitelisted)
  - `taskexec.go`: Predefined task execution with async support (only tasks in config are allowed)
  - `tasklog.go`: View task execution logs (running tasks, recent logs, read log contents)
  - `vpn.go`: VPN management tool (connect/disconnect/status/keep-alive via rasdial + Entra ID)
  - `memory.go`: Memory tools (memory_search, memory_write) - requires memory service
  - `identity.go`: Machine identity tool (always registered, returns configured machine name)
- **memory/**: HTTP client for LLMCrawl's memory service
  - `client.go`: HTTP client for memory service REST API
  - `flush.go`: 80% context flush detection and distillation parsing
- **pending/**: Persistent message queue for unsent replies (JSON file-backed, retry on startup)
- **mcp/**: Model Context Protocol server for exposing tools to Claude CLI
  - `server.go`: JSON-RPC stdio server implementation
  - `protocol.go`: MCP and JSON-RPC type definitions
- **whatsapp/**: WhatsApp Web integration via whatsmeow library
- **tasks/**: Task registry, executor with timeout, cron scheduler
- **config/**: YAML config loading with environment variable overrides
- **telemetry/**: OpenTelemetry traces + Prometheus metrics
- **tray/**: Windows system tray GUI

## Adding New Components

### Adding a Tool

1. Create new file in `src/tools/` (e.g., `mytool.go`)
2. Implement `tools.Tool` interface:
   ```go
   type Tool interface {
       Name() string                                      // Unique identifier
       Description() string                               // For LLM prompt
       Parameters() map[string]interface{}                // JSON Schema
       Execute(ctx context.Context, arguments string) (string, error)
   }
   ```
3. Register in `main.go` (line ~78-93):
   ```go
   if cfg.Tools.MyTool.Enabled {
       toolRegistry.Register(tools.NewMyTool(cfg.Tools.MyTool))
   }
   ```
4. Add config struct to `config/config.go` and `config.example.yaml`

**Important**: Tool `Execute()` receives arguments as a JSON string. Use `tools.ParseArgs[T]()` helper to unmarshal into a typed struct.

### Adding an LLM Provider

1. Create `src/llm/myprovider.go`
2. Implement `llm.Provider` interface:
   ```go
   type Provider interface {
       Name() string
       ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
   }
   ```
3. Add case in `llm.NewClient()` (client.go:92-110)
4. Add config struct to `config/config.go`

**Note**: The `llm` package uses a unified message format. Providers must translate to/from their native API formats. See `claude_cli.go` for Claude CLI subprocess integration, and `openai.go` for OpenAI's tool-calling translation.

### Adding a Task

Tasks are defined in `config.yaml` and can be scheduled (cron) or on-demand:

```yaml
tasks:
  my_task:
    description: "What this task does"
    command: "powershell -File C:\\scripts\\my_script.ps1"  # Optional
    timeout_seconds: 60
    schedule: "0 9 * * *"  # Optional cron schedule
```

Tasks without `command` are LLM-only interactions. Tasks with `command` execute external processes and optionally report results back through the LLM.

## Configuration

- Main config: `config.yaml` (copy from `config.example.yaml`)
- Environment variables override config values
- WhatsApp session stored in SQLite database (default: `whatsapp.db`)
- Logs written to `logging.file` (default: `officeclaw.log`)

**Critical config paths**:
- `whatsapp.trigger_prefix`: Message prefix for OfficeClaw agent mode (default: "OC:")
- `whatsapp.claude_trigger`: Message prefix for Claude CLI agent mode (default: "OCC:")
- Copilot CLI agent trigger is hardcoded as "OCCO:" (not configurable)
- `whatsapp.claude_working_folder`: Working directory for Claude CLI agent
- `whatsapp.copilot_working_folder`: Working directory for Copilot CLI agent
- `whatsapp.default_task`: Fallback task when none specified in OC: trigger message
- `whatsapp.machine_name`: Unique name for this machine (used for targeted messaging)
- `llm.provider`: "anthropic", "azure", or "openai"
- `tools.file_access.allowed_paths`: Whitelist for file read tool (security boundary)
- `tools.vpn.vpn_names`: Windows VPN connection names (first is default)
- `tools.memory.service_url`: Memory service URL (empty = disabled, see Memory Service section)
- `tasks.<name>.command`: Predefined command for task execution (only listed tasks are allowed)

### WhatsApp Setup

On first run, OfficeClaw displays a QR code. Scan it with your WhatsApp mobile app to link:

1. Open WhatsApp on your phone
2. Go to Settings → Linked Devices → Link a Device
3. Scan the QR code displayed in the terminal
4. Session is saved to `whatsapp.db` - subsequent runs reconnect automatically

### LLM Authentication Options

**Anthropic/Claude (Recommended)**: Uses the Claude Code CLI with organization SSO authentication. No API key required.

The Claude CLI must be installed and pre-authenticated via SSO (run `claude` once to authenticate). The agent auto-discovers the CLI from:
1. `CLAUDE_CLI_PATH` environment variable
2. `~/.claude-cli/currentVersion/claude.exe`
3. `~/.claude-cli/claude.exe`
4. System PATH

```yaml
llm:
  provider: "anthropic"
  anthropic:
    model: "claude-sonnet-4-20250514"
    max_tokens: 8192
    cli_path: ""  # Auto-detected if empty
```

**How it works**: The agent spawns the Claude CLI as a subprocess with `--output-format stream-json`. The CLI handles authentication via your organization's SSO. This mirrors the pattern used by LLMCrawl's Claude Bridge.

**Azure OpenAI**: Requires endpoint and either API key or Entra ID bearer token.

**OpenAI**: Requires `OPENAI_API_KEY` environment variable or `llm.openai.api_key` config.

**GitHub Copilot**: Uses the Copilot CLI with GitHub OAuth authentication. No API key required.

The Copilot CLI must be installed and pre-authenticated (run `copilot login`). Auto-discovers from:
1. `COPILOT_CLI_PATH` environment variable
2. WinGet links directory
3. `~/.copilot/bin/copilot.exe`
4. System PATH

```yaml
llm:
  provider: "copilot"
  copilot:
    model: ""         # Empty = Copilot default
    max_tokens: 8192
    cli_path: ""      # Auto-detected if empty
```

## Testing

- Tests live in `test/` directory
- Use `-count=1` to disable test caching: `go test ./test/... -v -count=1`
- Integration tests require valid credentials (set env vars or config)
- Mock LLM client available in test utilities

## Security Notes

- **WhatsApp session**: Stored in SQLite database. Keep `whatsapp.db` secure.
- **File access tool**: Restricted to `allowed_paths` whitelist. Validates all paths against whitelist before reading.
- **Task execution**: Only predefined tasks from `config.yaml` can be executed. The LLM cannot run arbitrary commands.
- **VPN tool**: Only VPN names listed in `tools.vpn.vpn_names` are allowed. Uses cached Entra ID tokens for silent auth.

## Windows-Specific Considerations

- System tray requires main thread: `tray.Run()` blocks in `main()` (tray/tray.go)
- Build with `-ldflags="-H windowsgui"` to hide console window
- Signal handling uses `syscall.SIGINT` and `syscall.SIGTERM`
- Paths in config use Windows backslashes: `C:\\Users\\...`

### Graceful Shutdown

On shutdown (signal or tray quit), OfficeClaw:

1. Stops accepting new WhatsApp messages
2. Cancels running Claude CLI sessions (30s timeout)
3. Waits for in-flight message handlers to complete (30s timeout)
4. Disconnects WhatsApp

### Pending Message Queue

If a reply cannot be sent (e.g., WhatsApp disconnected during shutdown), it is saved to `pending_messages.json`. On the next startup, pending messages are automatically retried after WhatsApp reconnects. Messages older than 24 hours are discarded.

## Telemetry

When enabled, Prometheus metrics exposed at `http://localhost:9090/metrics`:

- `officeclaw.messages.received`: Trigger messages received
- `officeclaw.messages.processed`: Messages successfully processed
- `officeclaw.llm.requests`: LLM API calls (labeled by provider, model, status)
- `officeclaw.llm.latency_seconds`: LLM call duration histogram
- `officeclaw.tools.calls`: Tool invocations (labeled by tool, status)
- `officeclaw.tasks.executed`: Task executions (labeled by task, status)

OpenTelemetry tracing is also available when `telemetry.otel.enabled` is true.

## Memory Service Integration

OfficeClaw integrates with LLMCrawl's standalone memory service for long-term memory across sessions. This is an optional feature that provides:

- **Conversation logging**: All messages are logged to daily markdown files
- **Semantic search**: Search past conversations using vector similarity
- **Durable facts**: Save important information to MEMORY.md for future sessions
- **Automatic distillation**: When context reaches 80%, extract summary and facts

### Setup

Deploy the memory service from the LLMCrawl repository:

```powershell
# From LLMCrawl repo
cd C:\src\github\LLMCrawl\memory-service

# REQUIRED: Set the path where conversation logs and MEMORY.md will be stored
$env:MEMORY_DATA_PATH = "C:\Users\you\OfficeClaw\memory"

# Optional: Change port (default 8007) or log level
$env:PORT = "8007"
$env:LOG_LEVEL = "INFO"

# Start memory service + Milvus
docker compose up -d

# Verify health
curl http://localhost:8007/health
```

**Environment variables:**
| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MEMORY_DATA_PATH` | **Yes** | - | Host path for logs/MEMORY.md |
| `PORT` | No | 8007 | Service port |
| `LOG_LEVEL` | No | INFO | Logging level |

For full API documentation, see `C:\src\github\LLMCrawl\docs\MEMORY.md`.

Configure OfficeClaw to use the memory service:

```yaml
tools:
  memory:
    service_url: "http://localhost:8007"
    flush_threshold: 0.8           # Trigger distillation at 80% context
    max_context_tokens: 100000     # Max tokens for flush detection
```

### Session Management

**OC: mode**: Session ID generated on process start (`oc-{timestamp}-{random}`). Use `/clear` command to start a new session.

**OCC: mode**: Uses Claude CLI's conversation_id as session ID. Use `/reset` to clear the session.

**OCCO: mode**: Uses Copilot CLI's sessionId as session ID. Use `/reset` to clear the session.

### Memory Tools

When memory service is available, two tools are registered for LLM use:

- `memory_search`: Search past conversations and facts semantically
- `memory_write`: Save durable facts to long-term memory

### Commands

OfficeClaw has a unified slash command system that works across all agent modes. Commands are sent as the message body after the trigger prefix (e.g., `OCC: /models`).

**All modes (OC:, OCC:, OCCO:)**:
- `/reset` — Clear session and start fresh
- `/model <name> [effort]` — Switch to a different model (effort levels for OCCO: only: low/medium/high/xhigh)
- `/models` — List available models for the current agent, with current model marked
- `/help` — Show available commands

**OC: mode only**:
- `/clear` — Clear conversation context and start a new session
- `/summary` — Force distillation to extract and save summary/facts

**OCCO: mode only**:
- `/effort <level>` — Set reasoning effort level (low/medium/high/xhigh) without changing model

**Per-chat model overrides**: Each chat can have its own model override via `/model`. The override persists until changed or the session is reset. The `/models` command shows the current model with a `*` marker.

### Graceful Degradation

If `service_url` is empty or the memory service is unreachable on startup, memory features are disabled. OfficeClaw continues to function normally without memory capabilities.
