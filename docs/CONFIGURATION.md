# OfficeClaw Configuration Reference

## Full Configuration

```yaml
# WhatsApp Integration
whatsapp:
  database_path: "whatsapp.db"    # SQLite database for session storage
  trigger_prefix: "OfficeClaw:"   # Prefix that activates the agent
  default_task: "assist"          # Task when none specified in trigger

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

Send a WhatsApp message starting with your trigger prefix:

```
OfficeClaw: <task_name> <message body>
```

Examples:
- `OfficeClaw: help me find files containing "TODO"`
- `OfficeClaw: summarize_files check the logs directory`
- `OfficeClaw: what's in my Documents folder?`

If no task name is provided, the `default_task` is used.
