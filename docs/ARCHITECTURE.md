# OfficeClaw Architecture

## Overview

OfficeClaw is an AI Agent system running as a Windows desktop application. It monitors WhatsApp for trigger messages, processes them through an LLM with tool-calling capabilities, and executes actions autonomously.

## System Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                        OfficeClaw                              │
│                                                                │
│  ┌──────────────────┐      ┌────────────────────────┐         │
│  │    WhatsApp      │      │    Task Scheduler      │         │
│  │    Listener      │      │    (cron-based)        │         │
│  │  (whatsmeow)     │      │                        │         │
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
│  ┌──────────────────┐  ┌────────────────────────┐             │
│  │  OpenTelemetry   │  │     System Tray        │             │
│  │  + Prometheus    │  │    (Windows GUI)       │             │
│  └──────────────────┘  └────────────────────────┘             │
└───────────────────────────────────────────────────────────────┘
```

## Package Structure

| Package     | Responsibility                                          |
|-------------|--------------------------------------------------------|
| `main`      | Entry point, dependency wiring, signal handling         |
| `config`    | YAML config loading, validation, env var overrides      |
| `whatsapp`  | WhatsApp Web integration via whatsmeow library          |
| `llm`       | Multi-provider LLM client (Claude CLI, Azure, OpenAI)   |
| `agent`     | Core agent loop: LLM ↔ tool-call orchestration          |
| `tools`     | Extensible tool registry + built-in tools               |
| `tasks`     | Task definitions, executor with timeout, cron scheduler |
| `tray`      | Windows system tray icon and menu                       |
| `telemetry` | OpenTelemetry tracing + Prometheus metrics              |

## Two Operating Modes

OfficeClaw supports two trigger prefixes (both case-insensitive):

### OC: Mode (OfficeClaw Agent)
Uses the custom OfficeClaw agent with tool orchestration:
1. WhatsApp listener detects a message starting with "OC:"
2. Message is parsed for task name (or uses default task)
3. Agent builds a prompt with message context and sends it to the LLM
4. LLM responds with text and/or tool calls
5. Tool calls are executed through the Tool Registry
6. Results are fed back to the LLM
7. Steps 4-6 repeat until the LLM provides a final text response (max 20 rounds)

### OCC: Mode (Claude CLI Agent)
Invokes Claude CLI directly as an autonomous agent with **session persistence via `--resume`**:
1. WhatsApp listener detects a message starting with "OCC:"
2. Claude CLI is spawned with `-p --dangerously-skip-permissions --resume <session-id>`
3. Claude CLI runs in the configured `claude_working_folder`
4. Claude executes autonomously using its built-in tools
5. Final response is sent back via WhatsApp
6. **Session ID is preserved** - subsequent OCC: messages use the same session ID to maintain context

**Session Management**:
- Each request spawns a new CLI process but uses `--resume` with the same session ID
- This maintains conversation context across requests
- Send `OCC: reset` (or configured keyword) to get a new session ID and start fresh

The OCC: mode bypasses the OfficeClaw agent loop entirely, giving Claude CLI full control with all permissions auto-approved.

## WhatsApp Integration

OfficeClaw uses the [whatsmeow](https://github.com/tulir/whatsmeow) library for WhatsApp Web integration:

1. First run: QR code displayed for scanning with WhatsApp mobile app
2. Session stored in SQLite database (`whatsapp.db`)
3. Subsequent runs: Auto-reconnects using saved session
4. Messages starting with trigger prefix are processed by the agent
5. Agent replies via the same WhatsApp chat

## LLM Integration

**Claude (Recommended)**: Uses the Claude Code CLI with organization SSO authentication:
- No API key required
- CLI spawned as subprocess with `--output-format stream-json`
- Handles authentication via your organization's SSO

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
