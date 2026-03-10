# OfficeClaw Configuration Reference

## Full Configuration

```yaml
# WhatsApp Integration
whatsapp:
  database_path: "whatsapp.db"    # SQLite database for session storage
  trigger_prefix: "OC:"           # Prefix for OfficeClaw agent mode
  claude_trigger: "OCC:"          # Prefix for direct Claude CLI agent mode
  claude_working_folder: "C:\\Projects\\MyRepo"  # Working folder for Claude CLI
  claude_session_reset_keyword: "reset"  # Keyword to reset session (e.g., "OCC: reset")
  default_task: "assist"          # Task when none specified in OC: trigger

# LLM provider
llm:
  provider: "anthropic"           # "anthropic", "azure", or "openai"
  temperature: 0.1
  request_timeout_seconds: 180

  # Claude via CLI (recommended - uses SSO auth, no API key needed)
  anthropic:
    model: "claude-sonnet-4-20250514"
    max_tokens: 8192
    cli_path: ""                  # Auto-detected if empty

  # Azure OpenAI
  azure:
    endpoint: ""                  # Or set AZURE_OPENAI_ENDPOINT
    api_key: ""                   # Or set AZURE_OPENAI_API_KEY
    api_version: "2024-02-01"

  # OpenAI direct
  openai:
    api_key: ""                   # Or set OPENAI_API_KEY
    model: "gpt-4"
    max_tokens: 8192

# Tools
tools:
  messaging:
    enabled: true                 # WhatsApp reply tool
  file_access:
    enabled: true
    allowed_paths:                # Whitelist for file read access
      - "C:\\Users\\you\\Documents"
    max_file_size_mb: 10
  task_execution:
    enabled: true
  vpn:
    enabled: true
    vpn_names:                    # Windows VPN connection names (first is default)
      - "MyVPN-1"
      - "MyVPN-2"
    connect_timeout_seconds: 30
    keep_alive_enabled: true      # Periodically reconnect if VPN drops
    keep_alive_minutes: 30
    # verify_path: "\\\\server\\share"  # Optional UNC path to verify connectivity

# Tasks (only predefined tasks can be executed)
tasks:
  assist:
    description: "General assistance"
    timeout_seconds: 300
  setupbuild:
    description: "Set up a new OS repository build"
    command: "c:\\tools\\setup-build.ps1 -branch main"
    timeout_seconds: 600
  # Custom tasks:
  # my_task:
  #   description: "Description for LLM to match against"
  #   command: "powershell -File script.ps1"
  #   timeout_seconds: 60
  #   schedule: "0 9 * * *"       # cron format

# Telemetry
telemetry:
  enabled: true
  prometheus:
    enabled: true
    port: 9090
    path: "/metrics"
  otel:
    enabled: true
    service_name: "officeclaw"

# Logging
logging:
  level: "info"
  file: "officeclaw.log"
  max_size_mb: 50
  max_backups: 3
```

## WhatsApp Setup

On first run, OfficeClaw displays a QR code in the terminal:

1. Open WhatsApp on your phone
2. Go to **Settings → Linked Devices → Link a Device**
3. Scan the QR code displayed in the terminal
4. Session is saved to `whatsapp.db`

Subsequent runs automatically reconnect using the saved session.

## LLM Provider Setup

### Claude (Recommended)

Uses the Claude Code CLI with your organization's SSO authentication:

1. Install Claude Code CLI
2. Run `claude` once to authenticate via SSO
3. OfficeClaw will auto-detect and use the CLI

No API key configuration needed.

```yaml
llm:
  provider: "anthropic"
  anthropic:
    model: "claude-sonnet-4-20250514"
    max_tokens: 8192
    cli_path: ""  # Auto-detected
```

### Azure OpenAI

```yaml
llm:
  provider: "azure"
  azure:
    endpoint: "https://your-resource.openai.azure.com/"
    api_key: "your-api-key"
    api_version: "2024-02-01"
```

Or use environment variables:
- `AZURE_OPENAI_ENDPOINT`
- `AZURE_OPENAI_API_KEY`

### OpenAI

```yaml
llm:
  provider: "openai"
  openai:
    api_key: "your-api-key"
    model: "gpt-4"
    max_tokens: 8192
```

Or set `OPENAI_API_KEY` environment variable.

## Environment Variable Overrides

| Env Var | Config Path |
|---------|-------------|
| `CLAUDE_CLI_PATH` | `llm.anthropic.cli_path` |
| `AZURE_OPENAI_ENDPOINT` | `llm.azure.endpoint` |
| `AZURE_OPENAI_API_KEY` | `llm.azure.api_key` |
| `OPENAI_API_KEY` | `llm.openai.api_key` |
| `WHATSAPP_DB_PATH` | `whatsapp.database_path` |

## Trigger Message Format

OfficeClaw supports two trigger modes (both case-insensitive):

### OC: Mode (OfficeClaw Agent)
Uses the OfficeClaw agent with custom tools (file access, task execution, messaging):

```
OC: <task_name> <message body>
```

Examples:
- `OC: help me find files containing "TODO"`
- `OC: summarize_files check the logs directory`
- `OC: what's in my Documents folder?`

If no task name is provided, the `default_task` is used.

### OCC: Mode (Claude CLI Agent)
Invokes Claude CLI directly as an autonomous agent with auto-approval of all tool requests.
Claude runs in the configured `claude_working_folder` with full tool access.

**Session Persistence**: Uses `--resume <session-id>` to maintain conversation context across messages. Each request spawns a new CLI process but uses the same session ID, so Claude remembers previous messages.

```
OCC: <request>
```

Examples:
- `OCC: refactor the main.go file`
- `OCC: analyze this codebase and suggest improvements`
- `OCC: help me debug the failing tests`
- `OCC: remember my name is Bob` → later: `OCC: what's my name?` (remembers "Bob")

**Resetting the Session**: Send the reset keyword to get a new session ID and start fresh:
```
OCC: reset
```
Response: "Session restarted. Conversation context has been cleared."

The reset keyword is configurable via `claude_session_reset_keyword` (default: "reset").

This mode bypasses the OfficeClaw agent and gives Claude CLI full autonomy.

## MCP Server (Model Context Protocol)

OfficeClaw includes an MCP server that exposes its custom tools to Claude CLI. This allows Claude to use OfficeClaw tools (read_file, execute_task, vpn_control) alongside its native tools.

### Running the MCP Server

The MCP server runs as a subprocess of Claude CLI using stdio transport:

```bash
officeclaw.exe mcp serve
```

The server reads configuration from `config.yaml` (or path specified by `CONFIG_PATH` environment variable).

### Configuring Claude CLI

Add the MCP server to your Claude CLI configuration:

**Option 1: Using claude mcp add command**
```bash
claude mcp add --transport stdio officeclaw -- C:\path\to\officeclaw.exe mcp serve
```

**Option 2: Manual configuration in ~/.claude.json**
```json
{
  "mcpServers": {
    "officeclaw": {
      "type": "stdio",
      "command": "C:\\path\\to\\officeclaw.exe",
      "args": ["mcp", "serve"],
      "env": {
        "CONFIG_PATH": "C:\\path\\to\\config.yaml"
      }
    }
  }
}
```

### Available MCP Tools

| Tool | Description |
|------|-------------|
| `read_file` | Read files from allowed directories (respects `tools.file_access.allowed_paths`) |
| `execute_task` | Execute predefined tasks (supports async execution for long-running tasks) |
| `view_task_log` | View task execution logs (list running tasks, find recent logs, read log contents) |
| `vpn_control` | Connect/disconnect VPN, check status |

**Note**: The `send_message` tool requires an active WhatsApp connection and is only available when running the full OfficeClaw application, not in standalone MCP mode.

### Automatic MCP in OCC: Mode

When using OCC: mode, OfficeClaw automatically configures itself as an MCP server for the spawned Claude CLI session. This means Claude CLI has access to all OfficeClaw tools without any manual MCP configuration. The MCP server is spawned as a child process and communicates via stdio.

### Verifying MCP Server

Test that the MCP server is working:

```bash
# List available tools
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | officeclaw.exe mcp serve

# In Claude CLI, check MCP status
/mcp
```
