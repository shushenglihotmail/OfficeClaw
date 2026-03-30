# OfficeClaw Technical Concerns

## 1. Security

### Path Traversal in FileAccessTool (HIGH)
**File:** `src/tools/fileaccess.go`
Simple string prefix matching for path allowlist. No symlink resolution (`filepath.EvalSymlinks`), no post-resolution validation. LLM could potentially escape allowed directories.

### Command Injection in PowerShell (CRITICAL)
**Files:** `src/tools/vpn.go`, `src/tasks/executor.go`
PowerShell commands built via `fmt.Sprintf` with string interpolation. VPN names and task commands are inserted without escaping. Config-defined values only, but still a risk surface.

### Secret Handling (MEDIUM)
**Files:** `src/config/config.go`, `src/llm/openai.go`, `src/llm/azure.go`
API keys stored in plaintext YAML config and environment variables. Not cleared from memory after use. No secret masking in logs.

### Open Chat Access by Default (MEDIUM)
**File:** `src/telegram/client.go`
If `allowed_chat_ids` is empty, ALL Telegram chats get access. Fail-open design.

## 2. Tech Debt

### Large Files
- `src/tasks/executor.go` (~650 lines) - mixes execution, scheduling, logging
- `src/telegram/client.go` (~650 lines) - mixes polling, routing, reconnection
- `src/agent/claude_agent.go` (~476 lines) and `copilot_agent.go` (~470 lines) - nearly identical code

### Code Duplication
- Claude and Copilot agents share ~80% identical logic (session management, CLI invocation, command handling)
- LLM providers have duplicated message conversion and error handling patterns

### Missing Abstractions
- No shared base for CLI agents (claude/copilot)
- No shared base for LLM providers
- No extracted session management

## 3. Testing Gaps

~15% test coverage. Critical untested paths:
- Agent orchestration loop (tool call parsing, error recovery)
- All LLM providers (response parsing, error handling)
- Telegram client (routing, reconnection, state management)
- VPN tool (rasdial invocation)
- Concurrency correctness (race conditions, goroutine cleanup)
- No integration or end-to-end tests

## 4. Performance

### Unbounded Message History
**File:** `src/agent/agent.go`
Conversation messages append without limit. Long conversations cause memory growth and slower LLM calls.

### Unbounded File Listing
**File:** `src/tasks/executor.go`
`FindLogFiles()` globs all `.log` files without pagination. Degrades with thousands of log files.

### Sequential Tool Execution
**File:** `src/agent/agent.go`
Tool calls executed one at a time in the agent loop. Independent calls could be parallelized.

### Goroutine Leaks in VPN Keep-Alive
**File:** `src/tools/vpn.go`
Keep-alive goroutine may not terminate cleanly if context outlives expected scope.

## 5. Fragile Areas

### CLI Output Parsing
**Files:** `src/llm/claude_cli.go`, `src/llm/copilot_cli.go`
Complex regex-based parsing of CLI JSON stream output. Fragile to CLI output format changes.

### PowerShell Execution
**File:** `src/tasks/executor.go`
Task execution depends on PowerShell availability and specific command syntax. No fallback.

### Shutdown Sequencing
**File:** `src/main.go`
Multiple goroutines, Telegram polling, CLI sessions, and pending queue must shut down in correct order. Race conditions possible.

## 6. Known Limitations

- UNC paths not tested in file access tool
- VPN dial timeout hardcoded
- No duplicate message detection
- Single-instance Telegram polling (no HA)
- No hot-reload of configuration
- No rate limiting on incoming messages
- No output truncation for large tool results

## 7. Dependencies

- `github.com/go-telegram-bot-api/telegram-bot-api/v5` - Telegram API (maintained)
- `github.com/getlantern/systray` - System tray (Windows-specific, limited maintenance)
- Claude CLI and Copilot CLI - External binaries, format changes break parsing
