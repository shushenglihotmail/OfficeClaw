# Codebase Concerns

**Analysis Date:** 2026-03-30

## Security

**File Access Path Traversal via Prefix Matching:**
- Risk: The file access whitelist in `src/tools/fileaccess.go` (line 84-93) uses `strings.HasPrefix` on the lowercased path. This means an allowed path of `C:\Users\sli` also permits access to `C:\Users\slike` or any path that starts with the same prefix but is a different directory.
- Files: `src/tools/fileaccess.go`
- Current mitigation: `filepath.Clean` is applied, which prevents `..` traversal. But the prefix check does not add a trailing separator.
- Fix approach: After `filepath.Clean`, ensure the allowed path ends with `\` (or `/`) before the prefix comparison, or check that the next character after the prefix match is a path separator. For example: `strings.HasPrefix(normalizedPath, normalizedAllowed + string(os.PathSeparator))` or exact match.

**VPN Tool Uses Global `log` Package:**
- Risk: The VPN tool in `src/tools/vpn.go` calls `log.Printf` (lines 138, 154, 181, etc.) instead of using the injected logger. This writes to whatever output the global logger is configured with, which in GUI mode is the log file but could be inconsistent.
- Files: `src/tools/vpn.go`
- Impact: Minor -- log messages may not appear in the expected output in all modes.
- Fix approach: Add a `*log.Logger` field to `VPNTool` and inject it during construction, consistent with how other components use the injected logger.

**Claude CLI Runs with `--dangerously-skip-permissions`:**
- Risk: The OCC: mode spawns Claude CLI with full auto-approval (`src/agent/claude_agent.go` line 223). This is by design for autonomous operation, but means any prompt injection via Telegram message could lead to arbitrary file/command access through Claude's built-in tools.
- Files: `src/agent/claude_agent.go`
- Current mitigation: `telegram.allowed_chat_ids` restricts who can send messages. If this list is empty, all chats are accepted (fail-open).
- Recommendation: Ensure `allowed_chat_ids` is always configured in production. Consider logging all prompts sent to Claude CLI for audit.

**Open Chat Access by Default:**
- Risk: If `telegram.allowed_chat_ids` is empty, ALL Telegram chats get access. This is a fail-open design.
- Files: `src/telegram/client.go` (line 205-209)
- Fix approach: Consider requiring at least one allowed chat ID, or logging a prominent warning at startup when the list is empty.

## Reliability

**CancelTask Removes from Running Map Before Goroutine Completes:**
- Issue: `CancelTask` in `src/tasks/executor.go` (line 190-202) calls `rt.Cancel()` and then immediately `delete(e.running, id)`. The async goroutine (line 441-509) also has a deferred `delete(e.running, taskID)` (line 446-449). After cancel, the goroutine still writes to the log file since it holds `logFile` and `cancel()` only cancels the context. The deferred delete will attempt to delete an already-deleted key (harmless no-op in Go). However, `ListRunningTasks()` will not show the task even though its goroutine may still be cleaning up and writing to the log.
- Files: `src/tasks/executor.go`
- Impact: Low -- functionally correct but semantically misleading. A user checking running tasks immediately after cancel will see the task gone, but it may still be writing its final log entries.
- Fix approach: Only call `rt.Cancel()` in `CancelTask` without deleting from the map. Let the goroutine's deferred cleanup handle removal. This keeps the task visible as "running" until it actually stops.

**Async Task Duplicate Detection is Check-Then-Act (TOCTOU):**
- Issue: In `src/tools/taskexec.go` (lines 136-144), the duplicate check iterates `ListRunningTasks()` then starts the async task. Between the check and the `ExecuteAsync` call, another goroutine could start the same task.
- Files: `src/tools/taskexec.go`, `src/tasks/executor.go`
- Impact: Low in practice -- the LLM tool-call loop is sequential per message, and concurrent messages from different chats are unlikely to trigger the same task simultaneously. But it is theoretically possible.
- Fix approach: Move duplicate detection into `ExecuteAsync` itself, under the executor's mutex, making the check-and-insert atomic.

**Pending Queue Non-Atomic File Writes:**
- Issue: `src/pending/queue.go` (line 131-147) writes the queue to disk using `os.WriteFile`, which is not atomic. If the process crashes mid-write, the file could be corrupted/truncated, losing all pending messages.
- Files: `src/pending/queue.go`
- Impact: Medium -- pending messages exist specifically for crash recovery, so corruption during a crash defeats their purpose.
- Fix approach: Write to a temp file first, then rename (atomic on most filesystems).

**Memory Flush Never Truncates Conversation (OC: Mode):**
- Issue: In `src/agent/agent.go`, the `messages` slice (line 84) accumulates all conversation messages. The flush/distillation mechanism at 80% context injects a distillation prompt, but the `messages` slice itself is never truncated after distillation. After the threshold is first crossed, every subsequent message will re-trigger flush detection since the total token estimate only grows.
- Files: `src/agent/agent.go`, `src/memory/flush.go`
- Impact: High for long-running OC: sessions -- the LLM receives a distillation prompt on every single message after the threshold is first crossed, degrading response quality and wasting tokens.
- Fix approach: After successful distillation, truncate `a.messages` to keep only a summary message, or reset the conversation with the distillation output as context.

**Handler Passes `context.Background()` to Message Handlers:**
- Issue: In `src/telegram/client.go` line 360, message handlers are invoked with `context.Background()` instead of a derived context. Handler goroutines for OC: mode cannot be cancelled via the parent context during shutdown.
- Files: `src/telegram/client.go`
- Impact: The `GracefulDisconnect` uses `c.wg.Wait()` with a 30s timeout, so orphaned handlers are eventually abandoned. But for OC: mode, the handler could run indefinitely if the LLM is stuck. OCC:/OCCO: modes have their own internal context cancellation via agent `Stop()`.
- Fix approach: Derive the handler context from a cancellable context that `GracefulDisconnect` can cancel.

## Scalability

**TaskExecutionTool `lastChatID` is Not Concurrency-Safe:**
- Issue: `src/tools/taskexec.go` line 23 has a `lastChatID string` field set via `SetChatID` (line 33) and read in the async completion callback closure (line 163). This field has no mutex protection.
- Files: `src/tools/taskexec.go`
- Impact: Medium -- if two messages from different chats arrive close together, `SetChatID` from the second message could overwrite the first before the first message's handler captures it in the closure. The async completion notification would then go to the wrong chat.
- Fix approach: Remove the `lastChatID` field. Pass the chat ID through context or as part of the tool call flow. The closure at line 163 captures `chatID` which comes from `t.lastChatID` at line 152, so the race window is between `SetChatID` and the closure capture.

**Cron Scheduler is a Simplified Implementation:**
- Issue: `src/tasks/executor.go` (lines 678-758) has a custom cron implementation that polls every minute. It does not support step values (`*/5`), month/weekday names, or other standard cron features. Uses `fmt.Sscanf` for number parsing which silently returns 0 on failure.
- Files: `src/tasks/executor.go`
- Impact: Users familiar with standard cron syntax may be surprised by missing features. The `fmt.Sscanf` silent failure means invalid cron fields match value 0 (midnight, Sunday, January 1st).
- Fix approach: Consider using `robfig/cron` or document the limited cron subset explicitly.

**Unbounded Log File Accumulation:**
- Issue: `FindLogFiles` in `src/tasks/executor.go` globs all `.log` files with no cleanup. Over time, the `logs/` directory can accumulate thousands of files. The bubble sort at line 275-281 is O(n^2) on the results.
- Files: `src/tasks/executor.go`
- Impact: Low -- would only matter after thousands of task executions.
- Fix approach: Add log retention/rotation policy. Use `sort.Slice` instead of bubble sort.

## Technical Debt

**Claude and Copilot Agents Are Nearly Identical:**
- Issue: `src/agent/claude_agent.go` (476 lines) and `src/agent/copilot_agent.go` (470 lines) share approximately 80% identical logic: session management, command handling, memory logging, reply sending, graceful shutdown. Only the CLI invocation and output parsing differ.
- Files: `src/agent/claude_agent.go`, `src/agent/copilot_agent.go`
- Fix approach: Extract a shared `CLIAgent` base struct with the common logic. Each specific agent only needs to implement CLI invocation and output parsing.

**Duplicate Truncation Helper Functions:**
- Issue: Three nearly identical string-truncation functions exist: `truncate` in `src/agent/agent.go` (line 365), `truncateForLog` in `src/agent/claude_agent.go` (line 470), and `truncateLog` in `src/telegram/client.go` (line 642).
- Files: `src/agent/agent.go`, `src/agent/claude_agent.go`, `src/telegram/client.go`
- Fix approach: Extract to a shared utility package.

**Local `min` Function:**
- Issue: `src/tools/taskexec.go` (line 242-247) defines a local `min` function. Go 1.21+ provides `min` as a builtin.
- Files: `src/tools/taskexec.go`
- Fix approach: Remove the local definition and use the builtin.

**Stale Naming in Agent `IncomingMessage`:**
- Issue: `src/agent/agent.go` (line 62-71) defines `IncomingMessage` with fields like `Source` (documented as "email" or "teams") and `Subject`, which are vestiges of a pre-Telegram architecture.
- Files: `src/agent/agent.go`
- Fix approach: Rename/simplify the struct to reflect Telegram-only usage. Remove unused `Subject` field.

## Risk Areas -- Fragile Code

**CLI Output Parsing:**
- Files: `src/agent/claude_agent.go` (`parseStreamJSONOutput`), `src/llm/claude_cli.go`, `src/llm/copilot_cli.go`
- Why fragile: These parsers depend on the exact JSON stream format from Claude CLI and Copilot CLI. Any change in CLI output format (new event types, changed field names, different JSONL structure) will silently produce empty or incorrect responses.
- Test coverage: Zero -- no tests exist for `parseStreamJSONOutput` or any CLI output parsing.
- Priority: High -- add unit tests with sample CLI outputs to catch format changes early.

**Log File Name Collision:**
- Issue: `createTaskLogFile` in `src/tasks/executor.go` (line 292-302) generates filenames using second-precision timestamps. If the same task starts twice within the same second, the second call overwrites the first log via `os.Create`.
- Files: `src/tasks/executor.go`
- Impact: Low -- duplicate detection prevents most cases, but scheduled tasks or manual overrides could collide.
- Fix approach: Append the task ID (UUID prefix) to the log filename.

## Test Coverage Gaps

**Core Agent Loop:**
- What's not tested: The LLM-tool-call loop in `src/agent/agent.go` `HandleMessage`, including multi-round tool execution, error recovery, and context flush.
- Files: `src/agent/agent.go`
- Risk: Regressions in the core processing pipeline go undetected.
- Priority: High

**CLI Agent Output Parsing:**
- What's not tested: `parseStreamJSONOutput` in `src/agent/claude_agent.go`, equivalent Copilot parsing.
- Files: `src/agent/claude_agent.go`, `src/agent/copilot_agent.go`
- Risk: CLI format changes silently break response extraction.
- Priority: High

**Telegram Message Routing:**
- What's not tested: Trigger prefix matching, machine targeting dispatch, shutdown rejection, mute behavior.
- Files: `src/telegram/client.go`
- Risk: Routing bugs could cause messages to be silently dropped or sent to wrong handlers.
- Priority: Medium

**Pending Queue Persistence:**
- What's not tested: Load/save cycle, corruption handling, age-based expiry, max attempts.
- Files: `src/pending/queue.go`
- Risk: Messages could be lost during crash recovery.
- Priority: Medium

**Concurrency Safety:**
- What's not tested: No race detector tests (`go test -race`) for the concurrent access patterns in session maps, running task maps, chat model overrides.
- Files: Multiple
- Risk: Data races under concurrent load.
- Priority: Medium

---

*Concerns audit: 2026-03-30*
