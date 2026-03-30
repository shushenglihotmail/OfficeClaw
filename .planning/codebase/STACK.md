# Technology Stack

**Analysis Date:** 2026-03-30

## Languages

**Primary:**
- Go 1.25.0 - Core application language, all source code in `src/` directory

## Runtime

**Environment:**
- Windows (primary platform, with system tray integration via `src/tray/tray.go`)
- Binary distribution as `officeclaw.exe`

**Build Target:**
- Windows GUI application (compiled with `-ldflags="-H windowsgui"` to hide console window)
- Console builds available for development (with `-o build/officeclaw.exe`)

## Package Manager

**Go Modules:**
- Lockfile: `go.mod` (required: Go 1.25.0+)
- `go.sum` present for dependency verification
- Commands: `go mod download`, `go mod tidy`

## Frameworks & Core Libraries

**Telegram Bot Integration:**
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1 - Telegram Bot API client
  - Location: `src/telegram/client.go`
  - Polling-based message reception (no webhooks)

**System Tray GUI:**
- `github.com/getlantern/systray` v1.2.2 - Windows system tray integration
  - Location: `src/tray/tray.go`
  - Blocks on main thread during `tray.Run()`

**OpenTelemetry (Observability):**
- `go.opentelemetry.io/otel` v1.24.0 - Distributed tracing
- `go.opentelemetry.io/otel/exporters/prometheus` v0.46.0 - Prometheus metrics exporter
- `go.opentelemetry.io/otel/sdk` v1.24.0 - OpenTelemetry SDK
- `go.opentelemetry.io/otel/trace` v1.24.0 - Tracing API
- Location: `src/telemetry/telemetry.go`

**Prometheus Metrics:**
- `github.com/prometheus/client_golang` v1.19.0 - Prometheus client library
  - Exposes metrics at `http://localhost:9090/metrics` (configurable)
  - Metrics for messages, LLM requests, tool calls, task execution

**Configuration:**
- `gopkg.in/yaml.v3` v3.0.1 - YAML parser for `config.yaml` loading
  - Location: `src/config/config.go`

**Utilities:**
- `github.com/google/uuid` v1.6.0 - UUID generation for session IDs

## Key Dependencies (Indirect)

**Prometheus Ecosystem:**
- `github.com/prometheus/client_model` v0.6.0 - Prometheus data model
- `github.com/prometheus/common` v0.48.0 - Prometheus utilities
- `github.com/prometheus/procfs` v0.12.0 - Linux `/proc` filesystem reader

**Go Logging & Observability:**
- `github.com/go-logr/logr` v1.4.1 - Structured logging interface
- `github.com/go-logr/stdr` v1.2.2 - Standard library logging adapter
- `google.golang.org/protobuf` v1.36.11 - Protocol Buffers (OpenTelemetry support)

**System Libraries:**
- `golang.org/x/sys` v0.41.0 - Windows system calls (signal handling, path operations)

**Testing & Development:**
- `github.com/stretchr/testify` v1.11.1 - Assertions and mocking (development only)

## Configuration

**Environment Variables (Overrides):**
- `TELEGRAM_BOT_TOKEN` - Telegram bot token from @BotFather
- `OPENAI_API_KEY` - OpenAI API key (if using OpenAI provider)
- `AZURE_OPENAI_ENDPOINT` - Azure OpenAI endpoint URL
- `AZURE_OPENAI_API_KEY` - Azure OpenAI API key
- `CLAUDE_CLI_PATH` - Path to Claude CLI executable (auto-detected if empty)
- `COPILOT_CLI_PATH` - Path to Copilot CLI executable (auto-detected if empty)
- `LOCALAPPDATA` - Windows environment variable used by Copilot CLI discovery
- `CONFIG_PATH` - Path to configuration file (defaults to `config.yaml`)

**Configuration Files:**
- `config.yaml` - Main configuration file (copy from `config.example.yaml`)
  - YAML format, loaded via `src/config/config.go`
  - Supports environment variable overrides for sensitive values

**Build Files:**
- `Makefile` - Build automation with targets: `build`, `build-console`, `run`, `test`, `test-coverage`, `lint`, `fmt`, `tidy`, `clean`, `deps`
- `go.mod` / `go.sum` - Go module manifest and lock file

## Platform Requirements

**Development:**
- Go 1.25.0 or later
- Windows system (for system tray and VPN tools)
- golangci-lint (for `make lint`)
- Git

**Production:**
- Windows operating system (tested on Windows 11 Enterprise)
- Telegram bot token (from @BotFather)
- One of: Claude CLI (SSO auth), OpenAI API key, Azure OpenAI credentials, or Copilot CLI (GitHub OAuth)
- Optional: Memory service (LLMCrawl) for long-term conversation memory

**CLI Tools (Optional but recommended):**
- Claude CLI (for `OC:` and `OCC:` modes) - Uses organization SSO authentication
- GitHub Copilot CLI (for `OCCO:` mode) - Uses GitHub OAuth
- PowerShell (for task execution via `command` field)

## Build Artifacts

**Output:**
- `build/officeclaw.exe` - Compiled binary (GUI version)
- `coverage.out` - Test coverage data (from `make test-coverage`)
- `coverage.html` - HTML coverage report

**Logging:**
- `officeclaw.log` - Main application log (path configurable via `logging.file`)
- Log rotation: max 50 MB per file, up to 3 backups (configurable)

## Telemetry & Observability

**Enabled by Default (configurable):**
- OpenTelemetry tracing (exporters configurable, no built-in receiver)
- Prometheus metrics at `http://localhost:9090/metrics` (port configurable)
- Service name: "officeclaw" (configurable)

**Metrics Exported:**
- `officeclaw.messages.received` - Trigger messages received
- `officeclaw.messages.processed` - Messages successfully processed
- `officeclaw.llm.requests` - LLM API calls (labeled by provider, model, status)
- `officeclaw.llm.latency_seconds` - LLM call duration histogram
- `officeclaw.tools.calls` - Tool invocations (labeled by tool, status)
- `officeclaw.tasks.executed` - Task executions (labeled by task, status)

## External Service Dependencies

**Runtime Services (all optional, with graceful degradation):**
- Telegram Bot API (`api.telegram.org`) - Message receiving and sending
- LLM providers:
  - Anthropic Claude (via Claude CLI subprocess with SSO)
  - OpenAI API (`api.openai.com`)
  - Azure OpenAI (custom endpoint)
  - GitHub Copilot (via Copilot CLI subprocess with OAuth)
- LLMCrawl Memory Service (optional, on `localhost:8007` by default)

---

*Stack analysis: 2026-03-30*
