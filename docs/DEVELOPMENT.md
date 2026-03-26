# OfficeClaw Development Guide

## Prerequisites

- Go 1.22+
- Windows (for system tray)
- Claude Code CLI (optional, for OCC: mode, authenticated via SSO)
- GitHub Copilot CLI (optional, for OCCO: mode, authenticated via `copilot login`)

## Build

```bash
# Console mode (shows stdout, for development)
make build-console

# Windows GUI mode (hides console, for production)
make build

# Run directly in development
make run
```

## Test

```bash
make test
```

## Project Layout

```
src/
├── main.go              # Entry point, dependency wiring, MCP subcommand
├── agent/               # Core agent orchestration
│   ├── agent.go         # OC: mode agent (LLM <-> tool loop)
│   ├── claude_agent.go  # OCC: mode (Claude CLI agent)
│   ├── copilot_agent.go # OCCO: mode (Copilot CLI agent)
│   └── commands.go      # Unified slash command system
├── telegram/            # Telegram Bot API integration (go-telegram-bot-api + reconnection)
├── config/              # Configuration (YAML + env overrides)
├── llm/                 # Multi-provider LLM clients
│   ├── client.go        # Unified client & Provider interface
│   ├── claude_cli.go    # Claude CLI provider (SSO auth)
│   ├── copilot_cli.go   # Copilot CLI provider (GitHub OAuth)
│   ├── azure.go         # Azure OpenAI provider
│   └── openai.go        # OpenAI API provider
├── tools/               # Extensible tool system
│   ├── registry.go      # Tool interface & registry
│   ├── messaging.go     # Telegram reply tool
│   ├── fileaccess.go    # Local file read tool
│   ├── taskexec.go      # Task execution tool (predefined only)
│   ├── tasklog.go       # Task log viewer
│   ├── vpn.go           # VPN management tool (rasdial + Entra ID)
│   ├── memory.go        # Memory search/write tools
│   └── identity.go      # Machine identity tool
├── mcp/                 # MCP server for CLI integration
├── memory/              # Memory service client
├── pending/             # Pending message queue (JSON file-backed)
├── tasks/               # Task management
│   └── executor.go      # Registry, executor, scheduler
├── tray/                # Windows system tray (interactive mode)
└── telemetry/           # OpenTelemetry + Prometheus
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
| `COPILOT_CLI_PATH` | `llm.copilot.cli_path` |
| `AZURE_OPENAI_ENDPOINT` | `llm.azure.endpoint` |
| `AZURE_OPENAI_API_KEY` | `llm.azure.api_key` |
| `OPENAI_API_KEY` | `llm.openai.api_key` |
| `TELEGRAM_BOT_TOKEN` | `telegram.bot_token` |

## Telegram Development

The Telegram integration uses [go-telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api):

- Bot token obtained from [@BotFather](https://t.me/BotFather)
- Set the token in `telegram.bot_token` in your `config.yaml`
- Restrict access by adding allowed chat IDs to `telegram.allowed_chat_ids`

## Metrics

When `telemetry.prometheus.enabled` is true, Prometheus metrics are exposed at `http://localhost:9090/metrics`:

- `officeclaw.messages.received` -- trigger messages received
- `officeclaw.llm.requests` -- LLM API calls (by provider, model, status)
- `officeclaw.llm.latency_seconds` -- LLM call duration
- `officeclaw.tools.calls` -- tool invocations (by tool, status)
- `officeclaw.tasks.executed` -- task executions (by task, status)

## Troubleshooting

### Telegram bot not responding
- Verify the bot token is correct in `config.yaml`
- Ensure the bot has not been revoked via @BotFather
- Check that the chat ID is in `telegram.allowed_chat_ids` (if the list is non-empty)
- Check OfficeClaw logs for connection errors

### Claude CLI not found
- OCC: mode will be unavailable (OfficeClaw still starts, replies with an error for OCC: messages)
- To enable: install Claude Code CLI and run `claude` to authenticate via SSO
- Set `CLAUDE_CLI_PATH` environment variable if auto-detection fails

### Copilot CLI not found
- OCCO: mode will be unavailable (OfficeClaw still starts, replies with an error for OCCO: messages)
- To enable: install GitHub Copilot CLI and run `copilot login` to authenticate
- Set `COPILOT_CLI_PATH` environment variable if auto-detection fails
