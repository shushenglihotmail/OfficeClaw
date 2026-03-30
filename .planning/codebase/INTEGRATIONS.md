# External Integrations

**Analysis Date:** 2026-03-30

## APIs & External Services

**Messaging:**
- Telegram Bot API - Core messaging platform
  - SDK/Client: `github.com/go-telegram-bot-api/telegram-bot-api/v5`
  - Auth: `TELEGRAM_BOT_TOKEN` env var or `telegram.bot_token` config
  - Integration: `src/telegram/client.go`
  - Protocol: HTTPS polling-based (no webhooks)
  - Max message length: 4096 characters (split automatically if needed)

**Language Models:**
- Anthropic Claude (via Claude CLI) - Default LLM provider
  - SDK/Client: Subprocess execution of Claude CLI
  - Auth: Organization SSO (pre-authenticated CLI session)
  - Location: `src/llm/claude_cli.go`
  - Auto-detection paths:
    - `CLAUDE_CLI_PATH` env var
    - `~/.claude-cli/currentVersion/claude.exe`
    - `~/.claude-cli/claude.exe`
    - System PATH
  - Protocol: CLI subprocess with `--output-format stream-json`
  - Supports: Tool-calling, streaming responses, conversation persistence

- OpenAI API - Optional LLM provider
  - SDK/Client: Direct HTTP client
  - Auth: `OPENAI_API_KEY` env var or `llm.openai.api_key` config
  - Location: `src/llm/openai.go`
  - Endpoint: `https://api.openai.com/v1/chat/completions`
  - Protocol: REST API (JSON)
  - Supports: Tool-calling, standard OpenAI message format

- Azure OpenAI / Azure Foundry - Optional LLM provider
  - SDK/Client: Direct HTTP client
  - Auth: API key (`AZURE_OPENAI_API_KEY`) or Entra ID bearer token
  - Location: `src/llm/azure.go`
  - Endpoint: Configured via `AZURE_OPENAI_ENDPOINT` env var or `llm.azure.endpoint`
  - Protocol: REST API with routing to multiple model deployments
  - Supports: OpenAI-compatible and Anthropic-compatible deployments
  - Model routing: `llm.azure.models` array maps logical names to deployment names

- GitHub Copilot CLI - Optional LLM provider (OCCO: mode)
  - SDK/Client: Subprocess execution of Copilot CLI
  - Auth: GitHub OAuth (pre-authenticated CLI session via `copilot login`)
  - Location: `src/llm/copilot_cli.go`
  - Auto-detection paths:
    - `COPILOT_CLI_PATH` env var
    - WinGet links directory
    - `~/.copilot/bin/copilot.exe`
    - System PATH
  - Protocol: CLI subprocess with `--output-format json` (JSONL) and session persistence
  - Supports: Tool-calling, multi-turn conversations via session ID

## Data Storage

**Databases:**
- None - OfficeClaw is stateless (no database required)

**File Storage:**
- Local filesystem only
  - Log files: `officeclaw.log` (configurable path)
  - Config: `config.yaml` (configurable path)
  - Pending message queue: `pending_messages.json` (JSON file-backed, for retry on restart)
  - Test coverage: `coverage.out`, `coverage.html`

**Caching:**
- In-memory conversation cache per session (duration: session lifetime)
- Entra ID token cache for VPN (managed by Windows credential system)

## Authentication & Identity

**Auth Providers:**
- Telegram Bot Token - Token-based authentication from @BotFather
  - Stateless (token is the only credential)
  - Scoped to single bot
  - Can be revoked via @BotFather

- Organization SSO (for Claude CLI)
  - Authentication: Pre-authenticated CLI session (run `claude` once to sign in)
  - Scope: Full access to Claude models via organization subscription
  - No API keys stored (CLI handles all auth)

- GitHub OAuth (for Copilot CLI)
  - Authentication: Pre-authenticated CLI session (run `copilot login`)
  - Scope: GitHub Copilot subscription access
  - No credentials stored (CLI handles OAuth flow)

- OpenAI API Keys
  - Authentication: Bearer token in Authorization header
  - Scope: Configurable per API key (organization, model restrictions)
  - Storage: `OPENAI_API_KEY` env var or config file (plaintext risk)

- Azure Entra ID (Optional for Azure OpenAI)
  - Authentication: Bearer token from Entra ID
  - Scope: Azure resource access
  - Token flow: Handled by Azure CLI or cached tokens

**Machine Identity:**
- Auto-detected from OS hostname (first segment of FQDN, lowercased)
- Used for message targeting with `@machine` syntax
- Tool: `get_identity` (always registered, returns hostname)
- Location: `src/tools/identity.go`

## Monitoring & Observability

**Error Tracking:**
- None (built-in) - Errors logged to file and stdout
- Integration ready: OpenTelemetry can export to external collectors

**Logs:**
- File-based logging to `officeclaw.log` (path configurable)
- Log rotation: Max 50 MB per file, up to 3 backups (configurable)
- Levels: debug, info, warn, error (configurable)
- Format: Standard Go log library output

**Metrics:**
- Prometheus HTTP endpoint at `http://localhost:9090/metrics` (port configurable, path: `/metrics`)
- Metrics types: Counter, Histogram
- Exports: Message counts, LLM call latency, tool execution stats, task execution stats
- Scrape format: Prometheus text format (OpenMetrics compatible)

**Distributed Tracing:**
- OpenTelemetry tracing framework
- Built-in provider but no active exporters
- OTLP endpoint configurable via `telemetry.otel.otlp_endpoint` (for external collectors)
- Service name: "officeclaw" (configurable)

## CI/CD & Deployment

**Hosting:**
- Windows desktop (no cloud hosting)
- System tray application (Windows-native GUI)
- Single-machine deployment

**CI Pipeline:**
- None configured (local development only)
- Makefile targets available for manual builds and testing

**Build Output:**
- `build/officeclaw.exe` (Windows GUI executable)
- Unsigned (no code signing configured)
- Distribution: Manual copy or deployment automation

## Environment Configuration

**Required env vars:**
- `TELEGRAM_BOT_TOKEN` - Telegram bot API token (from @BotFather)

**One of the following LLM providers required:**
- `CLAUDE_CLI_PATH` - Path to Claude CLI (if not using auto-detection)
- `OPENAI_API_KEY` - OpenAI API key
- `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_API_KEY` - Azure OpenAI credentials
- `COPILOT_CLI_PATH` - Path to Copilot CLI (if not using auto-detection)

**Optional env vars:**
- `CONFIG_PATH` - Path to config file (defaults to `config.yaml`)
- `LOCALAPPDATA` - Windows system variable (used by Copilot auto-detection)

**Secrets location:**
- Environment variables: `TELEGRAM_BOT_TOKEN`, `OPENAI_API_KEY`, `AZURE_OPENAI_API_KEY`
- Config file: `config.yaml` (plaintext - handle carefully)
- No `.env` file support; relies on OS environment or config file

## Webhooks & Callbacks

**Incoming:**
- None - Uses Telegram long-polling instead of webhooks

**Outgoing:**
- None - LLM communication is request-response only

## VPN Integration

**Windows VPN Management:**
- Tool: `vpn_control`
- Location: `src/tools/vpn.go`
- Command: Windows `rasdial.exe` for connection, PowerShell `Get-VpnConnection` for status
- Auth: Cached Entra ID / SSO tokens (silent auth)
- Timeout: 30 seconds (configurable)
- Keep-alive: Optional background reconnect loop (30 minutes configurable)
- Verify: Optional UNC path test to validate internal network access
- VPN names: Configured in `tools.vpn.vpn_names` (first is default)

## Memory Service Integration

**LLMCrawl Memory Service (Optional):**
- Service URL: `http://localhost:8007` (configurable via `tools.memory.service_url`)
- Client: HTTP client in `src/memory/client.go`
- Timeout: 30 seconds
- Protocol: REST API (JSON)
- Deployment: Docker Compose (from LLMCrawl repo)

**Endpoints:**
- `GET /health` - Health check (JSON response)
- `POST /search` - Semantic search in conversation history
- `POST /write` - Save facts to long-term memory
- `GET /context` - Get memory context for current session
- `POST /reindex` - Rebuild vector index
- `GET /logs` - List daily conversation logs

**Session Management:**
- OC: mode - Session ID generated at startup (`oc-{timestamp}-{random}`)
- OCC: mode - Claude CLI's `conversation_id`
- OCCO: mode - Copilot CLI's `sessionId`
- Reset: `/reset` command clears session and starts fresh

**Data Storage:**
- Memory data path: Host path mounted to Docker container (REQUIRED)
- Structure: Daily markdown logs + `MEMORY.md` for durable facts
- Vector embeddings: Milvus (deployed via Docker Compose with memory service)

## File Access Control

**File Read Tool:**
- Tool: `file_access`
- Location: `src/tools/fileaccess.go`
- Security: Path whitelist enforcement
- Allowed paths: Configured in `tools.file_access.allowed_paths`
- Max file size: 10 MB (configurable)
- Access: Read-only (no write access)

## Task Execution

**Predefined Tasks:**
- Tool: `task_execution`
- Location: `src/tools/taskexec.go`
- Security: Only tasks listed in `config.yaml` can be executed
- Commands: PowerShell commands (Windows native)
- Timeout: Per-task (configurable, default 300s)
- Execution: Synchronous with async result reporting
- Task logs: Viewable via `tasklog` tool

**Task Scheduler:**
- Cron scheduling: Optional `schedule` field in task definition
- Executor: `src/tasks/executor.go`
- Timezone: System timezone (no explicit config)

## Message Queuing

**Pending Message Queue:**
- Location: `pending_messages.json` (JSON file-backed)
- Purpose: Persist unsent replies across restarts
- Auto-retry: On startup after Telegram connects
- TTL: 24 hours (older messages discarded)
- Path: Relative to working directory

---

*Integration audit: 2026-03-30*
