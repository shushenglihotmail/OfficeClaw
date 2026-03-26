# OfficeClaw Architecture

## Overview

OfficeClaw is an AI Agent system running as a Windows desktop application. It monitors Telegram for trigger messages, processes them through LLMs with tool-calling capabilities, and executes actions autonomously.

## System Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                        OfficeClaw                              │
│                                                                │
│  ┌──────────────────┐      ┌────────────────────────┐         │
│  │    Telegram       │      │    Task Scheduler      │         │
│  │    Listener       │      │    (cron-based)        │         │
│  │(go-telegram-bot-  │      │                        │         │
│  │  api)             │      │                        │         │
│  └────────┬─────────┘      └───────────┬────────────┘         │
│           │                            │                       │
│           ├── "OC:" ──────┐            │                       │
│           │               ▼            │                       │
│           │     ┌─────────────────┐    │                       │
│           │     │ OfficeClaw Agent│◄───┘                       │
│           │     │  (orchestrator) │                            │
│           │     └────────┬────────┘                            │
│           │              │                                     │
│           │              ▼                                     │
│           │     ┌─────────────────┐                            │
│           │     │   LLM Client    │                            │
│           │     │  (Claude CLI    │   (as LLM bridge)          │
│           │     │   Azure/OpenAI) │                            │
│           │     └────────┬────────┘                            │
│           │              │ tool calls                          │
│           │              ▼                                     │
│           │     ┌─────────────────┐                            │
│           │     │  Tool Registry  │                            │
│           │     │  (Messaging,    │                            │
│           │     │   FileRead,     │                            │
│           │     │   TaskExec,     │                            │
│           │     │   VPNControl)   │                            │
│           │     └─────────────────┘                            │
│           │                                                    │
│           ├── "OCC:" ─────┐                                    │
│           │               ▼                                    │
│           │     ┌─────────────────┐                            │
│           │     │  Claude Agent   │   (session via --resume)   │
│           │     │  (auto-approval)│                            │
│           │     └────────┬────────┘                            │
│           │              │                                     │
│           │              ▼                                     │
│           │     ┌─────────────────┐                            │
│           │     │   Claude CLI    │   (full autonomy)          │
│           │     │  (working folder│                            │
│           │     │   configured)   │                            │
│           │     └─────────────────┘                            │
│           │                                                    │
│           ├── "OCCO:" ────┐                                    │
│           │               ▼                                    │
│           │     ┌─────────────────┐                            │
│           │     │ Copilot Agent   │   (session via --resume)   │
│           │     │  (--allow-all)  │                            │
│           │     └────────┬────────┘                            │
│           │              │                                     │
│           │              ▼                                     │
│           │     ┌─────────────────┐                            │
│           │     │  Copilot CLI    │   (full autonomy)          │
│           │     │  (working folder│                            │
│           │     │   configured)   │                            │
│           │     └─────────────────┘                            │
│           │                                                    │
│  ┌──────────────────┐  ┌────────────────────────┐             │
│  │  OpenTelemetry   │  │    System Tray           │             │
│  │  + Prometheus    │  │  (Windows GUI)         │             │
│  └──────────────────┘  └────────────────────────┘             │
└───────────────────────────────────────────────────────────────┘
```

## Package Structure

| Package     | Responsibility                                          |
|-------------|--------------------------------------------------------|
| `main`      | Entry point, dependency wiring, signal handling, MCP subcommand |
| `config`    | YAML config loading, validation, env var overrides      |
| `telegram`  | Telegram Bot API integration via go-telegram-bot-api, auto-reconnection |
| `llm`       | Multi-provider LLM client (Claude CLI, Copilot CLI, Azure, OpenAI) |
| `agent`     | Core agent loop, Claude/Copilot CLI agents, unified command system |
| `tools`     | Extensible tool registry + built-in tools               |
| `tasks`     | Task definitions, executor with timeout, cron scheduler |
| `mcp`       | MCP server for exposing tools to CLI agents             |
| `memory`    | HTTP client for LLMCrawl's memory service               |
| `pending`   | Persistent message queue for unsent replies              |
| `tray`      | Windows system tray icon and menu (interactive mode)     |
| `telemetry` | OpenTelemetry tracing + Prometheus metrics              |

## Three Operating Modes

OfficeClaw supports three trigger prefixes (all case-insensitive):

### OC: Mode (OfficeClaw Agent)
Uses the custom OfficeClaw agent with tool orchestration:
1. Telegram listener detects a message starting with "OC:"
2. Message is parsed for task name (or uses default task)
3. Agent builds a prompt with message context and sends it to the LLM
4. LLM responds with text and/or tool calls
5. Tool calls are executed through the Tool Registry
6. Results are fed back to the LLM
7. Steps 4-6 repeat until the LLM provides a final text response (max 20 rounds)

### OCC: Mode (Claude CLI Agent)
Invokes Claude CLI directly as an autonomous agent with **session persistence via `--resume`**:
1. Telegram listener detects a message starting with "OCC:"
2. Claude CLI is spawned with `-p --dangerously-skip-permissions --resume <session-id>`
3. OfficeClaw configures itself as MCP server via `--mcp-config`
4. Claude CLI runs in the configured `claude_working_folder`
5. Claude executes autonomously using its built-in tools + OfficeClaw tools via MCP
6. Final response is sent back via Telegram

### OCCO: Mode (Copilot CLI Agent)
Invokes GitHub Copilot CLI as an autonomous agent with **session persistence via `--resume`**:
1. Telegram listener detects a message starting with "OCCO:"
2. Copilot CLI is spawned with `-p --allow-all --output-format json --resume=<sessionId>`
3. OfficeClaw configures itself as MCP server via `--additional-mcp-config`
4. Copilot CLI runs in the configured `copilot_working_folder`
5. Copilot executes autonomously using its built-in tools + OfficeClaw tools via MCP
6. Final response extracted from JSONL output and sent back via Telegram

### Session Management
- Each request spawns a new CLI process but uses `--resume` with the same session ID
- This maintains conversation context across requests
- Send `/reset` to clear the session and start fresh

### Unified Command System
All modes support slash commands: `/reset`, `/model`, `/models`, `/help`.
OCCO: mode additionally supports `/effort` for reasoning effort levels.
OC: mode additionally supports `/clear` and `/summary`.

### Global Commands
Global commands are handled at the Telegram layer before agent dispatch, so they work regardless of which agent mode is used:
- `/mute` -- Mute this instance (only `/unmute` and `/ping` will be processed while muted)
- `/unmute` -- Unmute this instance
- `/ping` -- Show machine name, mute state, and available modes

Mute state is in-memory and resets to unmuted on restart. Machine targeting works with global commands (e.g., `OCC: @home /mute`).

### Machine Targeting
Messages can target specific machines: `OCC: @home hello` -- only the machine named "home" responds. Machine names are resolved automatically from the OS hostname (first segment of FQDN). Works with all trigger prefixes.

### Graceful Shutdown
On shutdown (signal or tray quit):
1. Stops accepting new Telegram messages
2. Cancels running CLI sessions (30s timeout)
3. Waits for in-flight message handlers to complete
4. Saves unsent replies to `pending_messages.json` for retry on next startup
5. Disconnects from Telegram

## Telegram Integration

OfficeClaw uses the [go-telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api) library for Telegram Bot API integration:

1. Bot token configured via `telegram.bot_token` (obtained from @BotFather)
2. Optionally restrict to specific chats via `telegram.allowed_chat_ids`
3. Bot polls for updates and processes messages starting with trigger prefixes
4. Agent replies via the same Telegram chat

## LLM Integration

**Claude (Recommended)**: Uses the Claude Code CLI with organization SSO authentication:
- No API key required
- CLI spawned as subprocess with `--output-format stream-json`
- Handles authentication via your organization's SSO

**GitHub Copilot**: Uses the Copilot CLI with GitHub OAuth authentication:
- No API key required
- CLI spawned as subprocess with `--output-format json` (JSONL)
- Supports reasoning effort levels (low/medium/high/xhigh)

**Azure OpenAI / OpenAI**: Traditional API key authentication

## Adding New Tools

Implement the `tools.Tool` interface:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, arguments string) (string, error)
}
```

Register in `main.go`:

```go
toolRegistry.Register(myNewTool)
```

## Adding New LLM Providers

Implement the `llm.Provider` interface:

```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```

Add a case in `llm.NewClient()` to instantiate the provider.

## Adding New Tasks

Add to `config.yaml`:

```yaml
tasks:
  my_task:
    description: "What this task does"
    command: "powershell -File C:\\scripts\\my_script.ps1"
    timeout_seconds: 60
    schedule: "0 9 * * *"     # optional cron schedule
```
