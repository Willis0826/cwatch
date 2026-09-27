# Verification report

Date: 2026-09-27. Host: MacBook with Apple silicon, macOS 15.5 (24F74), iTerm2 3.7.3, Claude Code 2.1.283, Go 1.27.1.

## Commands

| Command | Result |
|---|---|
| `go vet ./...` | Pass. No findings. |
| `go test ./...` | Pass. 11 packages. |
| `go test -race ./...` | Pass. 11 packages. |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build` | Pass. Mach-O arm64, 9.1 MB. |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build` | Pass. Mach-O x86_64, 9.4 MB. It was not run, because the host has no x86_64 machine. |
| `otool -L dist/cwatch-darwin-arm64` | Links only `libSystem` and `libresolv` (system libraries). |

Go was not installed on the host. The author used a Go 1.27.1 toolchain in a temporary directory. The module requires Go 1.26 or later.

## Hook reference check

The author read the hook reference at <https://code.claude.com/docs/en/hooks> on 2026-09-27 and compared it with Claude Code 2.1.283. These facts decide the design:

- Command hooks get JSON on stdin and run without a controlling terminal.
- Exec form (`command` plus `args`) runs without a shell.
- Exit code 0 with empty stdout is a success with no decision. For `SessionStart` and `UserPromptSubmit`, plain stdout becomes context for Claude, so the hook writes no stdout.
- `Stop` does not fire after a user interrupt. `StopFailure` fires instead of `Stop` after an API error.
- `PermissionRequest` has no `tool_use_id`.
- The `permission_prompt` notification comes about 6 seconds after the prompt. The `idle_prompt` notification comes about 60 seconds after a response.
- `SessionEnd` hooks share a 1.5-second budget. A per-hook `timeout` raises that budget.
- Subagent events carry `agent_id`.

## Acceptance tests

| # | Requirement | Test |
|---|---|---|
| 1 | Three sessions in one directory stay separate | `state.TestThreeSessionsSameDirectory` |
| 2 | One session ID in two TTYs stays separate | `state.TestSameSessionTwoTTYs` |
| 3 | Prompt → tool → permission → tool success → Stop | `state.TestPermissionFlow` |
| 4 | Stop gives idle; SessionEnd gives ended | `state.TestStopThenSessionEnd` |
| 5 | API failure and tool failure differ | `state.TestAPIFailureAndToolFailureDiffer` |
| 6 | Claude exits, shell stays: ended | `state.TestProcessExitWithShellOpen`; live check below |
| 7 | PID or TTY reuse does not revive or focus | `state.TestPIDReuseDoesNotRevive`, `app.TestFocusRefusals/process_gone,_tty_reused` |
| 8 | No lost updates under concurrency | `state.TestConcurrentWritersLoseNoUpdates` (with `-race`), `main.TestConcurrentHookProcesses` and `main.TestConcurrentFirstUse` (subprocesses), `state.TestRetryBusy` |
| 9 | Setup idempotent; uninstall keeps other settings; malformed file unchanged; special paths | `setup.TestInstallIdempotentAndUninstallPreserves`, `setup.TestUninstallRemovesOnlyOwned`, `setup.TestMalformedSettingsFail`, `setup.TestWriteSettings`, `app.TestSetupMalformedSettingsUnchanged`, `app.TestSetupDryRunCreatesNothing` |
| 10 | Bad JSONL records and escape sequences | `transcript.TestReadTailHandlesBadRecords`, `textutil.TestSanitizeRemovesEscapes`, `hooks.TestParseSanitizesAndBounds`, `tui.TestRenderSanitizesAndResizes` |
| 11 | Missing process data gives unknown, not a guess | `state.TestUnknownOwnerIsProvisional`, `process.TestFindOwnerNeverPicksTTYAncestor`, `app.TestFocusRefusals/unknown_owner` |
| 12 | Mock iTerm2 selects the exact pane; clear failures | `terminal.TestMatch`, `terminal.TestFocusAdapter`, `app.TestFocusAdapterErrors` |
| 13 | Dashboard shows persisted sessions | `app.TestLoadShowsPersistedSessions`, `main.TestListJSONAfterHooks`; live check below |
| 14 | No LLM, no network, no dashboard, no stdout decisions from the hook | `main.TestHookOutputContract`, `main.TestNoNetworkClientDependencies`, `main.TestBareCommandWithoutTerminalDoesNotStartDashboard`, `app.TestRunHookNeverFails` |

## Defect found during verification

The first benchmark lost 6 of 5,600 events. Several hook processes opened a new database at the same time. SQLite returned `SQLITE_BUSY` without a wait while the new database changed to WAL mode, so the busy timeout did not apply. The fix adds a bounded retry with jitter for lock errors in `Open` and `Record`. A failed transaction commits nothing, so a retry is safe. Without the fix, a harness showed 1 error in 600 hooks. With the fix, it showed 0 errors. The race is rare, so the subprocess stress test does not always trigger it. `state.TestRetryBusy` tests the retry itself. After the fix, the benchmark recorded 5,600 of 5,600 events.

## Live checks on this Mac

The author did these checks with a temporary state directory and a copy of the real settings file. The real `~/.claude/settings.json` did not change (SHA-1 prefix `0a2cb412e20c` before and after).

1. **Setup on a copy of the real settings.** The file already held hooks for 10 events from other tools. Setup added one group to each of the 10 cwatch events and kept all other groups and the key order. A second setup made no change. Uninstall restored content equal to the original.
2. **Real owner discovery.** A synthetic `UserPromptSubmit` event from a shell in this Claude Code session found the owner PID 99556 (`claude.exe`) through `CLAUDE_PID`, verified as an ancestor. It found `/dev/ttys001` and the iTerm2 pane UUID from `ITERM_SESSION_ID`. Liveness was `alive`.
3. **Real focus.** `cwatch focus 609dc103` selected iTerm2 window 86432, tab 2 (the current pane) in 0.41 s. macOS showed no Automation prompt, because access was already allowed.
4. **Real process exit.** A short-lived shell was the owner through `CLAUDE_PID`, then exited. `cwatch list` hid the instance. `cwatch list --all` showed `ended (process_exit)`. `cwatch focus` refused it with exit code 3.
5. **Real dashboard in a pseudo-terminal.** The dashboard showed the persisted instances, opened the details view, survived a resize from 110×30 to 60×20, and exited with status 0 after `q`. It entered and left the alternate screen.
6. **Doctor.** It reported the platform, executable, missing hooks (correct, because the real settings have none), tracking counts, iTerm2 running, and Claude Code 2.1.283.

## Outstanding manual check

The full manual check was not done. It needs real hooks in the real settings file, and the handover leaves that step to the user. Do these steps after `cwatch setup`:

1. Open three iTerm2 panes. Put two of them in the same repository. Start `claude` in each pane.
2. Submit a prompt in each pane. Make sure that `cwatch` shows three rows.
3. Cause a normal permission request. Make sure that the row shows `permission`, then `working` after approval.
4. Let a response finish. Make sure that the row shows `idle`.
5. Exit one Claude process and keep its shell. Make sure that its row leaves the default listing.
6. Push `Enter` on each remaining row. Make sure that iTerm2 focuses the correct pane.
7. Do steps 1–6 again with split panes and with a second iTerm2 window.

## Assumptions

- The executable name of Claude Code is `claude`, `claude.exe`, or starts with `claude-code`. The installed 2.1.283 uses `claude.exe` in the kernel process table. Claude Code 2.1.283 sets `CLAUDE_PID` in the environment of its Bash tool. The author did not verify that hook processes get it too. cwatch uses the value only when that process is an ancestor, and it falls back to the name match.
- `ITERM_SESSION_ID` holds `w<window>t<tab>p<pane>:<UUID>`. The UUID equals the AppleScript `id` of the iTerm2 session.
- A permission prompt on the main thread is modal. Thus a new main-thread tool call means that the earlier prompt was answered.
- A `SessionStart` event with source `compact` can come during a turn, so it does not reset the state.
