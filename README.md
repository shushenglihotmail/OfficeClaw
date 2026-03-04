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

Send a WhatsApp message to yourself starting with `OfficeClaw:` and the agent will process it:

```
OfficeClaw: what files are in my Documents folder?
OfficeClaw: summarize_files check the project logs
OfficeClaw: help me draft a response to the last email I received
```

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
