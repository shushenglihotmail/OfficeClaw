# OfficeClaw

An AI Agent system for Windows that monitors WhatsApp messages, processes them through LLM models, and executes tasks autonomously on your office machine.

## Features

- **24/7 Background Agent**: Runs as a desktop app with system tray icon
- **WhatsApp Integration**: Monitor and respond to messages via WhatsApp Web with auto-reconnection
- **Multi-Provider LLM**: Claude (via CLI with SSO), GitHub Copilot (via CLI), Azure OpenAI, OpenAI
- **Three Agent Modes**: OC: (custom agent), OCC: (Claude CLI), OCCO: (Copilot CLI) — all optional, graceful degradation
- **Extensible Tool System**: Reply to messages, read local files, execute tasks, view task logs, manage VPN
- **Task Execution Engine**: Predefined tasks with timeout, streaming logs, async execution, and WhatsApp notifications
- **MCP Server**: Exposes OfficeClaw tools to Claude CLI and Copilot CLI for seamless integration
- **Unified Command System**: `/model`, `/models`, `/reset`, `/effort` and more across all modes
- **Machine Targeting**: Route messages to specific machines when multiple instances share one WhatsApp account
- **Graceful Shutdown**: Pending message queue for reliability
- **Observability**: OpenTelemetry + Prometheus metrics

## How It Works

Send a WhatsApp message to yourself with a trigger prefix:

### OC: Mode (OfficeClaw Agent)
Uses the OfficeClaw agent with custom tools (file access, task execution, messaging):
```
OC: what files are in my Documents folder?
OC: summarize_files check the project logs
OC: run the backup task
```

### OCC: Mode (Claude CLI Agent)
Invokes Claude CLI directly as an autonomous agent with auto-approval of all tool requests:
```
OCC: refactor the main.go file to use dependency injection
OCC: analyze this codebase and suggest improvements
OCC: help me debug the failing test
```

### OCCO: Mode (Copilot CLI Agent)
Invokes GitHub Copilot CLI as an autonomous agent:
```
OCCO: review the latest changes and suggest improvements
OCCO: help me write unit tests for the agent package
```

Both CLI agents run in their configured working folders with full tool access. OfficeClaw automatically configures itself as an MCP server, giving the CLI access to OfficeClaw tools (task execution, file access, task logs, VPN) alongside its native tools.

### Slash Commands
All modes support slash commands sent after the trigger prefix:
```
OCC: /models              # List available Claude models
OCC: /model opus          # Switch to Opus
OCCO: /model gpt-5.4 high # Switch model with reasoning effort
OCCO: /effort xhigh       # Change reasoning effort only
OCC: /reset               # Clear session and start fresh
```

### Machine Targeting
When multiple OfficeClaw instances share one WhatsApp account:
```
OCC: @home refactor main.go      # Only "home" machine responds
OC: @home,office check disk      # Both respond
OC: hello                         # All machines respond
```

All triggers are **case-insensitive** (e.g., `oc:`, `OC:`, `Oc:` all work).

## Quick Start

```bash
# Build
make build-console

# Configure
cp config.example.yaml config.yaml
# Edit config.yaml if needed

# Run (with system tray)
./build/officeclaw.exe
```

On first run, scan the QR code with your WhatsApp mobile app to link the session.

## Project Structure

```
OfficeClaw/
├── src/                    # Source code
│   ├── main.go             # Entry point
│   ├── agent/              # Core agent orchestrator + CLI agents
│   ├── whatsapp/           # WhatsApp Web integration
│   ├── config/             # Configuration management
│   ├── llm/                # LLM provider integrations
│   ├── tools/              # Extensible tool system
│   ├── tasks/              # Task execution & scheduling
│   ├── mcp/                # MCP server for CLI integration
│   ├── memory/             # Memory service client
│   ├── pending/            # Pending message queue
│   ├── tray/               # Windows system tray
│   └── telemetry/          # OpenTelemetry + Prometheus
├── docs/                   # Documentation
├── test/                   # Tests
├── go.mod
├── Makefile
└── config.example.yaml
```

## Configuration

See [docs/CONFIGURATION.md](docs/CONFIGURATION.md) for full configuration reference.

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for system design details.

## Development

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for development setup and guidelines.

## License

Proprietary — Internal use only.
