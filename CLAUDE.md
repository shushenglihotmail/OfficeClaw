# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OfficeClaw is a Windows-native AI agent system that monitors WhatsApp for trigger messages, processes them through LLMs with tool-calling capabilities, and executes tasks autonomously. The agent runs as a 24/7 background service with a system tray interface.

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
```

## Architecture Overview

OfficeClaw supports two operating modes triggered by different prefixes (both case-insensitive):

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

### Package Responsibilities

- **main.go**: Dependency injection, startup sequence, signal handling, MCP subcommand
- **agent/**: Core orchestration loop, prompt building, conversation management
  - `claude_agent.go`: OCC: mode - Claude CLI integration with session persistence
- **llm/**: Multi-provider abstraction (Anthropic/Azure/OpenAI), unified message format
- **tools/**: Registry pattern for LLM tool-calling, execution dispatcher
  - `messaging.go`: WhatsApp reply tool
  - `fileaccess.go`: Local file read tool (path-whitelisted)
  - `taskexec.go`: Predefined task execution with async support (only tasks in config are allowed)
  - `tasklog.go`: View task execution logs (running tasks, recent logs, read log contents)
  - `vpn.go`: VPN management tool (connect/disconnect/status/keep-alive via rasdial + Entra ID)
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
- `whatsapp.claude_working_folder`: Working directory for Claude CLI agent
- `whatsapp.default_task`: Fallback task when none specified in OC: trigger message
- `llm.provider`: "anthropic", "azure", or "openai"
- `tools.file_access.allowed_paths`: Whitelist for file read tool (security boundary)
- `tools.vpn.vpn_names`: Windows VPN connection names (first is default)
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

## Telemetry

When enabled, Prometheus metrics exposed at `http://localhost:9090/metrics`:

- `officeclaw.messages.received`: Trigger messages received
- `officeclaw.messages.processed`: Messages successfully processed
- `officeclaw.llm.requests`: LLM API calls (labeled by provider, model, status)
- `officeclaw.llm.latency_seconds`: LLM call duration histogram
- `officeclaw.tools.calls`: Tool invocations (labeled by tool, status)
- `officeclaw.tasks.executed`: Task executions (labeled by task, status)

OpenTelemetry tracing is also available when `telemetry.otel.enabled` is true.
