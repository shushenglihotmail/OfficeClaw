# OfficeClaw

An AI Agent system for Windows that monitors WhatsApp messages, processes them through LLM models, and executes tasks autonomously on your office machine.

## Features

- **24/7 Background Agent**: Runs as a Windows desktop app with system tray icon
- **WhatsApp Integration**: Monitor and respond to messages via WhatsApp Web
- **Multi-Provider LLM**: Claude (via CLI with SSO), Azure OpenAI, OpenAI — extensible to more
- **Extensible Tool System**: Reply to messages, read local files, execute tasks, manage VPN
- **Task Execution Engine**: Predefined tasks with timeout, logging, and LLM reporting
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
Claude CLI runs in the configured `claude_working_folder` with full tool access.

**Persistent Session**: The session maintains context across messages. Send `OCC: reset` to clear context and start fresh.

Both triggers are **case-insensitive** (e.g., `oc:`, `OC:`, `Oc:` all work).

## Quick Start

```bash
# Build
make build-console

# Configure
cp config.example.yaml config.yaml
# Edit config.yaml if needed

# Run
./build/officeclaw.exe
```

On first run, scan the QR code with your WhatsApp mobile app to link the session.

## Project Structure

```
OfficeClaw/
├── src/                    # Source code
│   ├── main.go             # Entry point
│   ├── agent/              # Core agent orchestrator
│   ├── whatsapp/           # WhatsApp Web integration
│   ├── config/             # Configuration management
│   ├── llm/                # LLM provider integrations
│   ├── tools/              # Extensible tool system
│   ├── tasks/              # Task execution & scheduling
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
