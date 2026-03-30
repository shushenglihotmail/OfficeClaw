# OfficeClaw Codebase Structure

## Overview

OfficeClaw is a Windows desktop AI agent system (29 Go files) that monitors Telegram messages, processes them through LLM models with tool-calling capabilities, and executes tasks autonomously. The codebase is organized into 13 focused packages with clear separation of concerns.

## Directory Tree

```
/c/Users/sli/src/github/OfficeClaw/
├── build/                          # Compiled binaries
│   └── officeclaw.exe
├── docs/                           # Documentation
│   ├── ARCHITECTURE.md
│   ├── CONFIGURATION.md
│   └── DEVELOPMENT.md
├── logs/                           # Application logs
│   └── setupbuild-*.log
├── test/                           # Test files
│   ├── config_test.go
│   ├── machine_targeting_test.go
│   ├── tasks_test.go
│   ├── tools_test.go
│   └── logs/
├── src/                            # Main source code (13 packages)
│   ├── main.go                     # Entry point & dependency wiring
│   ├── agent/                      # Agent orchestration
│   ├── config/                     # Configuration management
│   ├── llm/                        # Multi-provider LLM clients
│   ├── telegram/                   # Telegram Bot API integration
│   ├── tools/                      # Extensible tool system
│   │   └── composition/            # PowerShell build composition scripts
│   ├── tasks/                      # Task execution & scheduling
│   ├── mcp/                        # MCP server (for CLI integration)
│   ├── memory/                     # Memory service client
│   ├── pending/                    # Pending message queue
│   ├── tray/                       # Windows system tray
│   └── telemetry/                  # OpenTelemetry + Prometheus
├── go.mod                          # Go module definition
├── go.sum                          # Go dependencies
├── Makefile                        # Build automation
├── README.md                       # User documentation
├── CLAUDE.md                       # Claude project notes
├── config.example.yaml             # Example configuration
├── config.yaml                     # Runtime configuration
└── officeclaw.log                  # Application log file
```

## Complete Go File Listing (29 files)

### Core Entry Point
- `src/main.go` - Application entry point, dependency wiring, signal handling, MCP subcommand router

### Agent Package (4 files)
- `src/agent/agent.go` - Core OC: mode agent with LLM orchestration loop
- `src/agent/claude_agent.go` - OCC: mode agent (Claude CLI with --resume sessions)
- `src/agent/copilot_agent.go` - OCCO: mode agent (Copilot CLI with --resume sessions)
- `src/agent/commands.go` - Unified slash command system (/reset, /model, /models, /help, /effort)

### Config Package (1 file)
- `src/config/config.go` - YAML configuration loading, validation, struct definitions

### LLM Package (5 files)
- `src/llm/client.go` - Multi-provider LLM client interface & router
- `src/llm/claude_cli.go` - Claude CLI provider (SSO authentication)
- `src/llm/copilot_cli.go` - Copilot CLI provider (GitHub OAuth)
- `src/llm/azure.go` - Azure OpenAI provider
- `src/llm/openai.go` - OpenAI API provider

### Telegram Package (1 file)
- `src/telegram/client.go` - Telegram Bot API integration (long polling, auto-reconnection, message routing)

### Tools Package (8 files)
- `src/tools/registry.go` - Tool interface, registry, dispatch system
- `src/tools/fileaccess.go` - Read-only local file access tool
- `src/tools/messaging.go` - Telegram reply tool
- `src/tools/taskexec.go` - Task execution tool (predefined tasks only)
- `src/tools/tasklog.go` - Task log viewer tool
- `src/tools/vpn.go` - VPN management tool (rasdial + Entra ID)
- `src/tools/memory.go` - Memory service search/write tools
- `src/tools/identity.go` - Machine identity tool

### Tasks Package (1 file)
- `src/tasks/executor.go` - Task registry, executor with timeout, cron scheduler

### MCP Package (2 files)
- `src/mcp/server.go` - MCP server implementation (stdio-based)
- `src/mcp/protocol.go` - MCP protocol types (JSON-RPC)

### Memory Package (2 files)
- `src/memory/client.go` - HTTP client for LLMCrawl memory service
- `src/memory/flush.go` - Conversation context flush/distillation

### Pending Package (1 file)
- `src/pending/queue.go` - Persistent message queue (JSON file-backed) for unsent Telegram replies

### Telemetry Package (1 file)
- `src/telemetry/telemetry.go` - OpenTelemetry tracing + Prometheus metrics

### Tray Package (1 file)
- `src/tray/tray.go` - Windows system tray icon and menu (interactive mode)

### Listeners Package (1 file)
- `src/listeners/listeners.go` - Message listener coordination

## Package Purposes

### `main.go` - Entry Point
Application initialization, dependency wiring, signal handling. Routes to MCP server mode if `mcp serve` subcommand is used.

### `agent/` - Agent Orchestration
Three distinct agent modes: OC: (custom LLM loop, max 20 rounds), OCC: (Claude CLI with `--resume`), OCCO: (Copilot CLI with `--resume`). Unified slash command parsing in `commands.go`.

### `config/` - Configuration Management
YAML config loading with environment variable overrides. Key structs: `Config`, `TelegramConfig`, `LLMConfig`, `ToolsConfig`, `Task`.

### `llm/` - Multi-Provider LLM Integration
Abstracted `Provider` interface with implementations for Claude CLI, Copilot CLI, Azure OpenAI, and OpenAI API. Standardized message format across all providers.

### `tools/` - Extensible Tool System
Registry pattern with `Tool` interface (Name, Description, Parameters, Execute). 8 built-in tools. Uses `ParseArgs[T]()` generic helper for JSON argument parsing.

### `tasks/` - Task Management
Task registry from config, execution with timeout, cron scheduling. Produces logs in `test/logs/`.

### `mcp/` - MCP Server
JSON-RPC 2.0 over stdio. Exposes OfficeClaw tools to Claude/Copilot CLIs via `tools/list` and `tools/call`.

### `memory/` - Memory Service Client
Optional HTTP client for LLMCrawl memory service. Graceful degradation if unreachable.

### `pending/` - Pending Message Queue
JSON file-backed queue for unsent Telegram replies. Auto-retry on startup, 24h expiry.

## File Naming Conventions

- **Package main file:** `{concept}.go` (e.g., `agent.go`, `client.go`, `server.go`)
- **Feature files:** `{feature}.go` (e.g., `fileaccess.go`, `messaging.go`, `vpn.go`)
- **Provider implementations:** `{provider}.go` or `{provider}_cli.go`
- **Test files:** `test/{package}_test.go` (separate `test/` directory)
- **Config files:** `config.yaml` (runtime), `config.example.yaml` (template)

## Where to Add New Code

### New Tool
1. Create `src/tools/{tool_name}.go` implementing `Tool` interface
2. Register in `main.go` under tool registration section
3. Add config struct to `src/config/config.go` and `config.example.yaml`

### New LLM Provider
1. Create `src/llm/{provider}.go` implementing `Provider` interface
2. Add case in `llm.NewClient()` router
3. Add config struct to `src/config/config.go`

### New Task
Define in `config.yaml` under `tasks:` section. Auto-registered on startup.

### New Agent Mode
1. Create `src/agent/{mode}_agent.go` with `MessageHandler` function
2. Register handler in `main.go`
3. Update trigger prefix detection in `src/telegram/client.go`
