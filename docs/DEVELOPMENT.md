# OfficeClaw Development Guide

## Prerequisites

- Go 1.22+
- Windows (for system tray)
- GCC compiler (for SQLite - required by go-sqlite3)
- Claude Code CLI (for Claude provider, authenticated via SSO)

## Build

```bash
# Console mode (shows stdout)
make build-console

# Windows GUI mode (hides console, runs in system tray)
make build

# Run directly
make run
```

## Test

```bash
make test
```

## Project Layout

```
src/
├── main.go           # Entry point & dependency wiring
├── agent/            # Core agent orchestration loop
├── whatsapp/         # WhatsApp Web integration (whatsmeow)
├── config/           # Configuration (YAML + env overrides)
├── llm/              # Multi-provider LLM clients
│   ├── client.go     # Unified client & Provider interface
│   ├── claude_cli.go # Claude CLI provider (SSO auth)
│   ├── azure.go      # Azure OpenAI provider
│   └── openai.go     # OpenAI API provider
├── tools/            # Extensible tool system
│   ├── registry.go   # Tool interface & registry
│   ├── messaging.go  # WhatsApp reply tool
│   ├── fileaccess.go # Local file read tool
│   ├── taskexec.go   # Task execution tool (predefined only)
│   └── vpn.go        # VPN management tool (rasdial + Entra ID)
├── tasks/            # Task management
│   └── executor.go   # Registry, executor, scheduler
├── tray/             # Windows system tray
│   └── tray.go
└── telemetry/        # OpenTelemetry + Prometheus
    └── telemetry.go
```

## Adding a New Tool

1. Create a new file in `src/tools/`
2. Implement the `Tool` interface:
   ```go
   type Tool interface {
       Name() string
       Description() string
       Parameters() map[string]interface{}
       Execute(ctx context.Context, arguments string) (string, error)
   }
   ```
3. Register it in `main.go` under the tool registration section

## Adding a New LLM Provider

1. Create a new file in `src/llm/` (e.g., `gemini.go`)
2. Implement the `Provider` interface
3. Add a case in `llm.NewClient()` to instantiate it
4. Add config struct in `src/config/config.go`

## Configuration

Copy `config.example.yaml` to `config.yaml`. Environment variables override config:

| Env Var | Config Path |
|---------|-------------|
| `CLAUDE_CLI_PATH` | `llm.anthropic.cli_path` |
| `AZURE_OPENAI_ENDPOINT` | `llm.azure.endpoint` |
| `AZURE_OPENAI_API_KEY` | `llm.azure.api_key` |
| `OPENAI_API_KEY` | `llm.openai.api_key` |
| `WHATSAPP_DB_PATH` | `whatsapp.database_path` |

## WhatsApp Development

The WhatsApp integration uses [whatsmeow](https://github.com/tulir/whatsmeow):

- Session stored in SQLite database (`whatsapp.db`)
- First run requires QR code scan
- Delete `whatsapp.db` to reset the session

## Metrics

When `telemetry.prometheus.enabled` is true, Prometheus metrics are exposed at `http://localhost:9090/metrics`:

- `officeclaw.messages.received` — trigger messages received
- `officeclaw.llm.requests` — LLM API calls (by provider, model, status)
- `officeclaw.llm.latency_seconds` — LLM call duration
- `officeclaw.tools.calls` — tool invocations (by tool, status)
- `officeclaw.tasks.executed` — task executions (by task, status)

## Troubleshooting

### WhatsApp not connecting
- Delete `whatsapp.db` and restart to get a new QR code
- Ensure your phone has internet connectivity
- Check if WhatsApp Web is logged out on your phone

### Claude CLI not found
- Ensure Claude Code CLI is installed
- Run `claude` manually to authenticate via SSO
- Set `CLAUDE_CLI_PATH` environment variable if auto-detection fails

### Build fails with CGO errors
- Install GCC (e.g., via MSYS2 or TDM-GCC on Windows)
- go-sqlite3 requires CGO for compilation
