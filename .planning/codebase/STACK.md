# Technology Stack

**Analysis Date:** 2026-03-30

## Languages

**Primary:**
- Go 1.25.0 - All application code in `src/` directory (`go.mod` line 3)

**Secondary:**
- PowerShell - Task commands executed via `os/exec` (configured in `config.yaml` tasks section)
- YAML - Configuration format (`config.yaml`, `config.example.yaml`)

## Runtime

**Environment:**
- Go 1.25.0+ (minimum version in `go.mod`)
- Windows 10/11 (Windows-native desktop application with system tray)
- Binary: `build/officeclaw.exe`

**Package Manager:**
- Go Modules (`go.mod` / `go.sum`)
- Lockfile: `go.sum` present for dependency verification

## Frameworks & Core Libraries

**Telegram Bot Integration:**
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1 - Telegram Bot API client
  - Location: `src/telegram/client.go`
  - Polling-based message reception (no webhooks)

**System Tray GUI:**
- `github.com/getlantern/systray` v1.2.2 - Windows system tray integration
  - Location: `src/tray/tray.go`
  - Blocks on main thread during `tray.Run()` (Windows GUI requirement)

**Configuration:**
- `gopkg.in/yaml.v3` v3.0.1 - YAML parser for `config.yaml`
  - Location: `src/config/config.go`

**Utilities:**
- `github.com/google/uuid` v1.6.0 - UUID generation for task IDs and session IDs
  - Used in: `src/tasks/executor.go`, `src/llm/claude_cli.go`

## Observability Stack

**OpenTelemetry:**
- `go.opentelemetry.io/otel` v1.24.0 - Distributed tracing API
- `go.opentelemetry.io/otel/sdk` v1.24.0 - OTel SDK (traces + metrics)
- `go.opentelemetry.io/otel/exporters/prometheus` v0.46.0 - Prometheus exporter bridge
- `go.opentelemetry.io/otel/metric` v1.24.0 - Metrics API
- `go.opentelemetry.io/otel/trace` v1.24.0 - Tracing API
- Location: `src/telemetry/telemetry.go`

**Prometheus:**
- `github.com/prometheus/client_golang` v1.19.0 - Prometheus client library
- Exposes metrics at `http://localhost:9090/metrics` (port/path configurable)
- Metrics: message counts, LLM latency, tool call stats, task execution stats

## Key Dependencies (Indirect)

- `github.com/prometheus/client_model` v0.6.0 - Prometheus data model
- `github.com/prometheus/common` v0.48.0 - Prometheus utilities
- `github.com/go-logr/logr` v1.4.1 - Structured logging interface (OTel)
- `golang.org/x/sys` v0.41.0 - Windows system calls (signals, paths)
- `google.golang.org/protobuf` v1.36.11 - Protocol Buffers (OTel support)
- `github.com/stretchr/testify` v1.11.1 - Test assertions (indirect)

## Build System

**Build Tool:** GNU Make (`Makefile`)

**Commands:**
```bash
make build            # Production: go build -ldflags="-H windowsgui" -o build/officeclaw.exe ./src
make build-console    # Development: go build -o build/officeclaw.exe ./src (with console output)
make run              # go run ./src
make test             # go test ./test/... -v -count=1
make test-coverage    # Coverage report -> coverage.out, coverage.html
make lint             # golangci-lint run ./...
make fmt              # gofmt -s -w .
make deps             # go mod download && go mod tidy
make clean            # rm -rf build/ coverage.out coverage.html
```

**Output Artifacts:**
- `build/officeclaw.exe` - Windows GUI executable (or console build)
- `coverage.out` / `coverage.html` - Test coverage (from `make test-coverage`)
- `officeclaw.log` - Runtime log file (path configurable)

## Configuration

**Primary config:** `config.yaml` (copy from `config.example.yaml`)
- YAML format, loaded in `src/config/config.go` via `config.Load()`
- Environment variable overrides applied in `applyEnvOverrides()`
- Sensible defaults applied in `applyDefaults()`
- Validation in `Config.Validate()`

**Environment Variables (override config.yaml values):**
- `TELEGRAM_BOT_TOKEN` - Telegram bot token
- `AZURE_OPENAI_ENDPOINT` - Azure OpenAI endpoint
- `AZURE_OPENAI_API_KEY` - Azure OpenAI API key
- `OPENAI_API_KEY` - OpenAI API key
- `CLAUDE_CLI_PATH` - Path to Claude Code CLI executable
- `COPILOT_CLI_PATH` - Path to GitHub Copilot CLI executable
- `CONFIG_PATH` - Config file path (MCP server mode only)

## Development Tools

**Linting:**
- `golangci-lint` - Run via `make lint`

**Formatting:**
- `gofmt -s -w .` - Run via `make fmt`

**Testing:**
- Go standard `testing` package
- Tests in `test/` directory (separate from `src/`)
- `github.com/stretchr/testify` for assertions

## Platform Requirements

**Development:**
- Go 1.25.0+
- Windows 10/11 (system tray and VPN tools are Windows-native)
- golangci-lint (for `make lint`)
- Git

**Production:**
- Windows 10/11 desktop (system tray application, single-machine deployment)
- Telegram bot token from @BotFather
- At least one LLM provider: Claude CLI (SSO), OpenAI API key, Azure OpenAI, or Copilot CLI (GitHub OAuth)
- Optional: Docker for LLMCrawl memory service

**CLI Tools (auto-detected, optional):**
- Claude Code CLI - For `OC:` mode (Anthropic provider) and `OCC:` mode (direct Claude)
- GitHub Copilot CLI - For `OCCO:` mode (direct Copilot)
- PowerShell - For task command execution

## Telemetry

**Prometheus Metrics (configurable):**
- `officeclaw.messages.received` - Trigger messages received
- `officeclaw.messages.processed` - Messages successfully processed
- `officeclaw.llm.requests` - LLM API calls (by provider, model, status)
- `officeclaw.llm.latency_seconds` - LLM call duration histogram
- `officeclaw.tools.calls` - Tool invocations (by tool, status)
- `officeclaw.tasks.executed` - Task executions (by task, status)

**OpenTelemetry Tracing:**
- Service name: "officeclaw" (configurable)
- OTLP endpoint configurable for external collectors

---

*Stack analysis: 2026-03-30*
