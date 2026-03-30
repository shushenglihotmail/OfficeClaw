# External Integrations

**Analysis Date:** 2026-03-30

## APIs & External Services

### Telegram Bot API

- **Purpose:** Core messaging platform (input/output for all agent modes)
- **SDK:** `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1
- **Auth:** Bot token from @BotFather
- **Config:** `telegram.bot_token` or `TELEGRAM_BOT_TOKEN` env var
- **Integration:** `src/telegram/client.go`
- **Protocol:** HTTPS long-polling (no webhooks)
- **Features:** Message receiving, reply sending, chat ID access control (`telegram.allowed_chat_ids`)
- **Max message length:** 4096 characters (split automatically)
- **Reconnection:** Watchdog in `tgClient.StartReconnectWatchdog(ctx)`

### LLM Providers (Multi-Provider Abstraction)

All providers implement the `llm.Provider` interface in `src/llm/client.go`:

```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```

**Anthropic Claude (via Claude CLI):**
- Purpose: Default LLM provider for OC: mode
- Client: CLI subprocess with `--output-format stream-json`
- Auth: Organization SSO (pre-authenticated CLI session)
- Location: `src/llm/claude_cli.go`
- Auto-detection: `CLAUDE_CLI_PATH` env > `~/.claude-cli/currentVersion/claude.exe` > `~/.claude-cli/claude.exe` > PATH
- Default model: `claude-sonnet-4-20250514`
- Supports: Tool-calling, streaming responses

**OpenAI API:**
- Purpose: Optional LLM provider
- Client: Direct HTTP (`net/http`)
- Auth: `OPENAI_API_KEY` env var or `llm.openai.api_key` config
- Location: `src/llm/openai.go`
- Endpoint: `https://api.openai.com/v1/chat/completions`
- Supports: Tool-calling, standard OpenAI message format

**Azure OpenAI / Azure Foundry:**
- Purpose: Optional LLM provider with multi-model routing
- Client: Direct HTTP (`net/http`)
- Auth: API key (`AZURE_OPENAI_API_KEY`) or Entra ID bearer token
- Location: `src/llm/azure.go`
- Endpoint: `AZURE_OPENAI_ENDPOINT` env var or `llm.azure.endpoint`
- Model routing: `llm.azure.models` array maps logical names to deployment names
- Supports: OpenAI-compatible and Anthropic-compatible deployments

**GitHub Copilot CLI:**
- Purpose: Optional LLM provider for OCCO: mode
- Client: CLI subprocess with `--output-format json` (JSONL)
- Auth: GitHub OAuth (pre-authenticated via `copilot login`)
- Location: `src/llm/copilot_cli.go`
- Auto-detection: `COPILOT_CLI_PATH` env > WinGet links > `~/.copilot/bin/copilot.exe` > PATH
- Session persistence: `--resume=<sessionId>`

### Claude CLI Agent (OCC: Mode)

- Purpose: Direct Claude CLI invocation with full autonomy
- Location: `src/agent/claude_agent.go`
- Config: `telegram.claude_trigger` (default: "OCC:"), `telegram.claude_working_folder`
- Features: Session persistence via `conversation_id`, auto-configured MCP server for OfficeClaw tools
- Spawns Claude CLI with `--dangerously-skip-permissions`

### Copilot CLI Agent (OCCO: Mode)

- Purpose: Direct Copilot CLI invocation with full autonomy
- Location: `src/agent/copilot_agent.go`
- Config: `telegram.copilot_working_folder`
- Trigger: Hardcoded "OCCO:" (not configurable)
- Features: Session persistence via `sessionId`, MCP server via `--additional-mcp-config`
- Spawns Copilot CLI with `--allow-all`

## Data Storage

**Databases:**
- None - OfficeClaw is stateless (no database required)

**File Storage (local filesystem):**
- `config.yaml` - Application configuration
- `officeclaw.log` - Runtime logs (configurable path, rotation: 50MB x 3 backups)
- `pending_messages.json` - Persistent queue for unsent Telegram replies (`src/pending/queue.go`)
- Task log files - In `task_logs/` directory, one per execution (`src/tasks/executor.go`)

**Caching:**
- In-memory conversation context per session (agent lifetime)
- Windows credential cache for Entra ID tokens (VPN tool)

## Authentication & Identity

**Telegram Bot Token:**
- Stateless token-based auth from @BotFather
- Config: `telegram.bot_token` or `TELEGRAM_BOT_TOKEN` env var

**Claude CLI SSO:**
- Pre-authenticated CLI session (run `claude` once to sign in)
- No API keys stored; CLI handles org SSO flow

**Copilot CLI OAuth:**
- Pre-authenticated CLI session (run `copilot login`)
- No credentials stored; CLI handles GitHub OAuth

**OpenAI API Key:**
- Bearer token in Authorization header
- Config: `OPENAI_API_KEY` env var or `llm.openai.api_key`

**Azure Entra ID:**
- Bearer token from Entra ID (for Azure OpenAI and VPN)
- Cached tokens managed by Azure CLI / Windows credential system

**Machine Identity:**
- Auto-detected from OS hostname (first segment of FQDN, lowercased)
- Used for `@machine` targeting syntax in messages
- Tool: `get_identity` in `src/tools/identity.go` (always registered)

## VPN Integration

- **Tool name:** `vpn_control`
- **Location:** `src/tools/vpn.go`
- **Windows commands:** `rasdial.exe` (connect/disconnect), PowerShell `Get-VpnConnection` (status)
- **Auth:** Cached Entra ID / SSO tokens (silent auth; manual sign-in when expired)
- **Config:** `tools.vpn.vpn_names` (first is default), `tools.vpn.connect_timeout_seconds`
- **Keep-alive:** Optional background reconnect loop (`tools.vpn.keep_alive_enabled`, default 30 min interval)
- **Verify:** Optional UNC path test (`tools.vpn.verify_path`) to validate internal network access

## Memory Service (LLMCrawl)

- **Purpose:** Long-term semantic memory across sessions (optional)
- **Client:** HTTP client in `src/memory/client.go` (30s timeout)
- **Config:** `tools.memory.service_url` (default: `http://localhost:8007`)
- **Deployment:** Docker Compose from LLMCrawl repo (requires `MEMORY_DATA_PATH` env var)
- **Graceful degradation:** If unreachable at startup, memory features disabled silently

**REST Endpoints Used:**
- `GET /health` - Health check
- `POST /search` - Semantic search in conversation history
- `POST /write` - Save facts to MEMORY.md
- `GET /context` - Get memory context for session
- `POST /reindex` - Rebuild vector index
- `GET /logs` - List daily conversation logs

**Context Flush:**
- Location: `src/memory/flush.go`
- Trigger: When context reaches `tools.memory.flush_threshold` (default 80%) of `tools.memory.max_context_tokens` (default 100K)
- Action: Extract summary and durable facts via distillation

**Session IDs:**
- OC: mode - `oc-{timestamp}-{random}` (generated at startup)
- OCC: mode - Claude CLI's `conversation_id`
- OCCO: mode - Copilot CLI's `sessionId`

## Task Execution

- **Tool name:** `execute_task`
- **Location:** `src/tools/taskexec.go`
- **Executor:** `src/tasks/executor.go`
- **Security:** Only tasks defined in `config.yaml` are allowed
- **Commands:** PowerShell or other Windows commands via `os/exec`

**Execution modes:**
- **Synchronous:** Default for tasks with timeout <= 180s
- **Asynchronous:** Auto-enabled for tasks with timeout > 180s (`AsyncThreshold` in `src/tools/taskexec.go`)
- **Duplicate control:** `allow_duplicate` config per task (default: false, prevents concurrent runs)
- **Live monitoring:** `monitoring_interval_seconds` config per task; streams console output to Telegram at configured interval via `tasks.MonitorConfig`
- **Cancel:** `execute_task` with `action: "cancel"` calls `Executor.CancelTask()` to stop running tasks

**Cron scheduling:**
- Optional `schedule` field (cron expression) per task
- Scheduler runs in `taskExecutor.StartScheduler(ctx)` goroutine

**Task logs:**
- Tool: `view_task_log` in `src/tools/tasklog.go`
- Actions: list running tasks, list recent logs, read log contents

## MCP Server

- **Purpose:** Expose OfficeClaw tools to Claude CLI sessions
- **Location:** `src/mcp/server.go`, `src/mcp/protocol.go`
- **Protocol:** JSON-RPC over stdio (MCP protocol version `2024-11-05`)
- **Invocation:** `officeclaw.exe mcp serve`
- **Tools exposed:** file_access, execute_task, view_task_log, vpn_control, get_identity, memory_search, memory_write
- **Not exposed in MCP mode:** send_message (requires Telegram client)

## Monitoring & Observability

**Logging:**
- File-based: `officeclaw.log` (configurable path)
- Rotation: Max 50MB per file, 3 backups (configurable)
- Levels: debug, info, warn, error
- Format: Go standard `log` package with `[OfficeClaw]` prefix

**Prometheus Metrics:**
- Endpoint: `http://localhost:9090/metrics` (port/path configurable)
- Counters: messages received/processed, LLM requests, tool calls, task executions
- Histograms: LLM call latency

**OpenTelemetry Tracing:**
- Service name: "officeclaw" (configurable)
- OTLP endpoint: configurable for external collectors (`telemetry.otel.otlp_endpoint`)

**Error Tracking:**
- None (built-in) - errors logged to file; OTel available for external export

## CI/CD & Deployment

**Hosting:** Windows desktop (single-machine, system tray application)
**CI Pipeline:** None configured (local development with Makefile)
**Build Output:** `build/officeclaw.exe` (unsigned)

## Webhooks & Callbacks

**Incoming:** None (Telegram uses long-polling)
**Outgoing:** None (all communication is request-response)

## Environment Configuration Summary

**Required:**
- `TELEGRAM_BOT_TOKEN` (or `telegram.bot_token` in config)

**One LLM provider (at least one recommended):**
- Anthropic: Claude CLI installed and SSO-authenticated
- OpenAI: `OPENAI_API_KEY` env var
- Azure: `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_API_KEY`
- Copilot: Copilot CLI installed and GitHub-authenticated

**Optional:**
- `CONFIG_PATH` - Config file path (default: `config.yaml`)
- `CLAUDE_CLI_PATH` / `COPILOT_CLI_PATH` - Override CLI auto-detection
- Memory service at `tools.memory.service_url`

**Secrets location:**
- Environment variables (preferred for sensitive values)
- `config.yaml` (plaintext - handle carefully)
- No `.env` file support

---

*Integration audit: 2026-03-30*
