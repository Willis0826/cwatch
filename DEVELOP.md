# cwatch: developer guide

This guide explains how cwatch works, how to build it, and how to test it. For the install and uninstall steps, see [README.md](README.md). For the test results and the verification report, see [docs/VERIFICATION.md](docs/VERIFICATION.md).

## Requirements

- macOS on arm64 or amd64.
- iTerm2, for focus. The list and the dashboard work in all terminals.
- Claude Code with exec-form command hooks (`command` plus `args`). The author checked cwatch with Claude Code 2.1.283.
- Go 1.26 or later, to build from source.

## Build

```sh
make build        # writes bin/cwatch (CGO_ENABLED=0)
make dist         # writes dist/cwatch-darwin-arm64 and dist/cwatch-darwin-amd64
make test         # go test ./...
make race         # go test -race ./...
make vet          # go vet ./...
make clean        # delete bin/ (keeps the prebuilt dist/ binaries)
```

To make the README images again, run `make screenshots`. This target needs Go, Python 3, and Google Chrome. It renders the real dashboard with demo data (`internal/tui/demo_test.go`), converts the ANSI output to HTML (`scripts/ansi2png.py`), and takes a screenshot with headless Chrome. It writes `docs/images/dashboard.png` and `docs/images/details.png`.

## What `make setup` does

This command does these steps:

1. It installs the binary. When Go is available, it builds the binary first. Else it uses the prebuilt `dist/cwatch-darwin-<arch>`. The install directory is the first writable directory of `/opt/homebrew/bin` and `/usr/local/bin`. If neither directory is writable, it uses `~/.local/bin`.
2. It shows the hook changes (`cwatch setup --dry-run`). This step changes no files. The output shows the target settings file, the hook command, and the new settings content.
3. It asks for confirmation. If you answer `n`, it stops and changes no settings.
4. It installs the hooks (`cwatch setup`) with the installed binary. Thus the hooks use the absolute path of the installed binary and do not depend on your shell `PATH`.
5. It runs `cwatch doctor`.

Options:

| Variable | Function |
|---|---|
| `BINDIR=DIR` | Install the binary in `DIR`. |
| `YES=1` | Do not ask for confirmation. |
| `NO_EXCERPTS=1` | Store no prompt or response excerpts. |
| `SETTINGS=FILE` | Edit a different Claude Code settings file. |

Example: `make setup BINDIR=$HOME/.local/bin NO_EXCERPTS=1`.

Do not move or delete the installed binary. The hooks contain its path. If you move it, run `make setup` again.

After the install, restart the Claude Code sessions that already run. A running session does not load new hooks. Then submit a prompt in each session. cwatch tracks a session only after it receives a hook event from that session. Then start the dashboard:

```sh
cwatch
```

The separate steps are also available: `make install`, `make hooks-check`, `make hooks`, and `make doctor`. Setup refuses a temporary binary, for example a binary from `go run`.

## What setup changes

Setup edits only the user settings file of Claude Code: `~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json` when you set that variable. Use `--settings-file` to select a different file. Setup adds one matcher group to each of these events: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Notification`, `Stop`, `StopFailure`, and `SessionEnd`. Each group holds one handler:

```json
{
  "type": "command",
  "command": "/Users/you/.local/bin/cwatch",
  "args": ["hook", "--managed-by=cwatch", "--state-dir", "/Users/you/.cwatch"],
  "timeout": 5
}
```

The handler uses exec form (`args`), so Claude Code starts cwatch without a shell. Paths with spaces or apostrophes need no quotes. The `SessionEnd` handler has no `timeout`, so it keeps the 1.5-second default budget of Claude Code and never delays the exit.

Setup obeys these rules:

- It keeps all other settings, hooks, and environment entries. It keeps the key order.
- It stops with an error if the settings file is not valid JSON. It does not change a malformed file.
- It writes a backup (`settings.json.cwatch-backup-<time>`) before each change.
- It writes a temporary file and renames it over the settings file. It keeps the file mode, and it follows a symbolic link to its target.
- It stops if another program changed the settings file during the edit.
- A second run makes no change.
- It finds its own handlers by the `--managed-by=cwatch` argument. It also finds older handlers whose executable is named `cwatch` and whose first argument is `hook`. It finds them at all executable paths, so a moved binary is replaced, not duplicated.

## What `make uninstall` does

`make uninstall` runs `cwatch uninstall` with the installed binary. If that binary does not exist, it uses `bin/cwatch` or `dist/cwatch-darwin-<arch>`. Any cwatch binary can remove the hooks, because uninstall finds the cwatch hooks at all executable paths. Then it deletes the installed binary.

Uninstall removes only the handlers that cwatch owns. It removes a matcher group only when that group held only cwatch handlers. It never removes all hooks of an event. It keeps the history in the state directory.

## Commands

| Command | Function |
|---|---|
| `cwatch` | Open the dashboard. It does no setup. |
| `cwatch list` | Print a table of live instances. |
| `cwatch list --json` | Print versioned JSON (`schema_version: 1`). |
| `cwatch list --all` | Include ended instances. |
| `cwatch focus <id>` | Focus the iTerm2 pane of an instance. A unique prefix of the ID is sufficient. |
| `cwatch setup [--dry-run]` | Install the hooks. |
| `cwatch uninstall [--dry-run]` | Remove the hooks. Keep the history. |
| `cwatch doctor` | Check the platform, executable, settings, hooks, state, iTerm2, and Claude Code version. |
| `cwatch hook` | Internal. Claude Code runs it. It reads one event from stdin. |
| `cwatch version` | Print the version. |

All commands accept `--state-dir DIR` and `--settings-file FILE`. `--no-excerpts` hides excerpts in `list` and in the dashboard.

Exit codes: `0` success, `1` error, `2` usage error, `3` focus refused because the target is not safe.

### Dashboard keys

| Key | Function |
|---|---|
| `↑`/`k`, `↓`/`j` | Select an instance. |
| `Enter` | Focus the pane of the selected instance. |
| `/` | Filter by project, directory, branch, state, TTY, or ID. `Enter` keeps the filter. `Esc` clears it. |
| `→` or `d` | Show details: full path, identifiers, excerpts, and recent events. `d` also closes them. |
| `←` or `Esc` | Close the details. |
| `a` | Show or hide ended instances. |
| `r` | Refresh now. |
| `q`, `Ctrl+C` | Quit. |

The dashboard sets the terminal title to the state counts, for example `🟡1 🟢2 ⚪1 · cwatch`. The states that need attention come first. The dashboard clears the title when it exits. iTerm2 can add the job name, for example `(cwatch)`, to the title. To remove it, clear **Job** in **Settings > Profiles > General > Title**.

The dashboard refreshes once each second. It keeps the selected instance across refreshes. It focuses a pane only when you push `Enter`, never during a refresh.

## States

A state tells what the hooks observed. It never tells that a task succeeded.

| Evidence | State |
|---|---|
| `SessionStart` (startup, resume, clear, fork) | `idle` |
| `SessionStart` with source `compact` | no change (compaction can occur during a turn) |
| `UserPromptSubmit` | `working` |
| `PreToolUse` on the main thread | `working`; cwatch records the tool |
| `PermissionRequest` | `needs_permission` (shown as `permission`) |
| `Notification` of type `permission_prompt`, while `working` | `needs_permission` |
| `PostToolUse` that matches the pending tool | `working` |
| `PostToolUseFailure` | no state change; cwatch records the failure, because Claude can recover |
| `Stop` on the main thread | `idle` |
| `Notification` of type `idle_prompt`, while `working` | `idle`, unless a prompt arrived in the last 10 seconds |
| `StopFailure` (API error) | `error`, with the error type and a bounded detail |
| `SessionEnd` | `ended`, reason `session_end:<reason>` |
| The owning process no longer exists, or its PID has a new start time | `ended`, reason `process_exit` |
| The owning process could not be identified | the event state, with liveness `unknown` (shown with `?`) |

Other rules:

- An event with an `agent_id` comes from a subagent. A subagent event never makes an idle main session busy again. A subagent `Stop` never makes the main session idle.
- An ended instance ignores late notifications and tool events. Only `SessionStart` or `UserPromptSubmit` from the same process revives it. An instance that ended with `process_exit` stays ended.
- A new main-thread `PreToolUse` clears pending main-thread permission prompts, because those prompts are modal. When a prompt still waits, the `permission_prompt` notification (about 6 seconds later) sets `needs_permission` again.
- When a process starts a new session (for example after `/clear` or `/resume`), cwatch ends the rows of the earlier sessions of that process with reason `superseded`.
- cwatch never uses silence or age to decide that a session ended or that a task is complete.

## Identity

cwatch stores an `instance_id` for each row. The instance ID is not the Claude `session_id`. The identity of an instance is the session ID, the owning process (PID and process start time), and the terminal.

- The hook walks at most 12 ancestor processes with `sysctl`. It does not start `ps`. It selects the process named by the `CLAUDE_PID` variable when that process is an ancestor. Else it selects the nearest ancestor whose executable name is `claude`, `claude.exe`, or `claude-code…`. It never selects an ancestor only because that ancestor has a terminal. cwatch records the method in `owner_method`.
- The TTY is the controlling terminal of the owning process. The hook process has no terminal, so cwatch does not read the TTY from the hook.
- The hook records the iTerm2 pane UUID from `ITERM_SESSION_ID`. It records `tmux` when `TMUX` is set, and `ssh` when `SSH_CONNECTION` or `SSH_TTY` is set. The hook reads only named variables (these, `CLAUDE_PID`, `HOME`, and `CWATCH_DEBUG`). It stores no environment.
- The same session ID in two terminals gives two rows. A resumed conversation in a new process gives a new row.
- When the owner is unknown, cwatch keeps a provisional row for the session and the pane. When a later event identifies the owner, cwatch adopts that row. It does not add a second row.

## Focus

`cwatch focus` and `Enter` in the dashboard do these checks first:

1. The instance is not ended.
2. The terminal kind is `iterm2`. cwatch refuses tmux, SSH, and other terminals.
3. The owning process is known, it runs, and it has the same start time. A reused PID fails this check.
4. The current TTY of that process is the recorded TTY.

cwatch then lists the iTerm2 panes with a fixed AppleScript through `osascript`. It passes values only as script arguments. It selects the pane with the recorded pane UUID and checks that the pane has the same TTY. When no UUID is recorded, exactly one pane must have the TTY. cwatch never selects a pane by project path or window title. The scripts check that iTerm2 runs before they address it, so they never start iTerm2.

The first focus can ask for macOS Automation access to iTerm2. If you refused it, allow it in **System Settings > Privacy & Security > Automation**.

## Token usage

cwatch reads token usage from the session transcript. It makes no model calls and no network requests. Claude Code writes the API usage of each response into the transcript. cwatch counts each response once by its message ID, because Claude Code can write one response as several records. cwatch counts only the main thread. It does not count subagents that write their own transcript files, synthetic messages, or sidechain records.

The dashboard and `list` read the usage, never the hook. The dashboard keeps the read offset of each transcript, so a refresh reads only new lines. On the development Mac, the first read of a 35 MB transcript took 48 ms, and a later refresh took less than 1 ms. One read takes at most 64 MiB. A larger backlog is read on later refreshes, and the context size shows `…` until then.

`cwatch list --json` puts the values in a `tokens` object: `context_tokens`, `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`, `responses`, and `model`. cwatch never stores these values. It reads them again each time.

Limits: Claude Code writes the transcript asynchronously, so the values can be a few seconds late. After `/compact` or `/clear`, the context size drops, but the totals keep counting. The transcript format is internal to Claude Code and can change. When cwatch finds no usage, it shows `—`.

## Storage and privacy

- The state directory is `~/.cwatch/` (mode 0700). The database is `~/.cwatch/cwatch.db` (mode 0600).
- The store is SQLite in WAL mode with the pure-Go driver `modernc.org/sqlite`. Each hook writes the event and the updated instance in one `BEGIN IMMEDIATE` transaction. The busy timeout is 3 seconds. A lock error that SQLite returns without a wait gets a bounded retry.
- cwatch stores normalized event fields only: event name, tool name, tool-use ID, agent ID, notification type, and error type. It does not store tool input, tool output, the environment, or the raw hook payload.
- cwatch stores the latest prompt (at most 1,000 characters) and the latest final response text from `Stop` (at most 2,000 characters). These excerpts can contain sensitive text. They stay on this computer. Use `cwatch setup --no-excerpts` to store none, and `--no-excerpts` on `list` or the dashboard to show none.
- The details view reads at most the last 256 KiB of the transcript. It reads the transcript as data only.
- cwatch removes control characters and ANSI escape sequences from all text before it stores or shows that text.
- Retention: `list` and the dashboard delete events older than 14 days, keep at most 50,000 events, and delete ended instances older than 30 days. The hook never deletes data.
- Hook errors go to stderr (the Claude Code debug log) and to `~/.cwatch/hook-errors.log` (at most about 256 KiB, one rotated copy).

## Hook behavior

The hook reads at most 4 MiB from stdin. It discards the rest. It stops after 3 seconds. It always exits with code 0 and writes nothing to stdout, so it never blocks Claude Code, never adds context, and never makes a permission decision. It ignores unknown events and unknown fields. Set `CWATCH_DEBUG=1` to log ignored events.

Measured on the development Mac (Apple silicon, macOS 15.5), with the release build:

| Case | Median | p95 | Max |
|---|---|---|---|
| One hook at a time, store with 5,000 events | 11.8 ms | 16.3 ms | 18.2 ms |
| 8 concurrent hook processes, 2 shared instances | 16.2 ms | 24.4 ms | 79.8 ms |
| Process start only (`cwatch version`) | 8.7 ms | 11.7 ms | 13.4 ms |

These values include process start. Most of the time is process start and the initialization of the SQLite driver. The values on other computers can be different.

## Known limitations

- **Existing sessions:** cwatch sees a session only after that session loads the hooks and sends an event. Restart sessions that ran before setup.
- **Interrupts:** Claude Code sends no `Stop` event after a user interrupt. An interrupted session shows `working` until the next event. The `idle_prompt` notification (about 60 seconds later) corrects the state, if you did not type in that session.
- **Denied permission:** Claude Code sends no event when you deny a permission prompt. cwatch clears the prompt at the next main-thread tool call, at `Stop`, or at the next prompt.
- **Other dialogs:** MCP elicitation dialogs and `agent_needs_input` notifications do not change the state. cwatch records them in the details view.
- **Ordering:** Claude Code sends no sequence number or timestamp with hook events. cwatch orders events by the local receive order (`seq`). Two hooks that Claude Code starts at the same time can arrive in a different order. `PreToolUse` and `PermissionRequest` give no shared identifier, so cwatch correlates a permission request with a tool by tool name and agent ID.
- **Background subagents:** Tool events from a subagent that runs after the main turn stops do not make the session `working`. cwatch records them as subagent activity.
- **Unsupported focus targets:** tmux, SSH, Terminal.app, and other terminals. cwatch lists these sessions but refuses focus.
- **Platform:** macOS only. On other platforms, process checks fail and liveness is `unknown`.
- **Settings scope:** Setup edits the user settings file only. It does not edit project settings or managed settings. If `allowManagedHooksOnly` is set, Claude Code does not run the cwatch hooks.
- **Concurrent edit check:** Setup compares the file content just before the rename. A very short window stays between that check and the rename.

## Project layout

| Package | Function |
|---|---|
| `cmd/cwatch` | Command dispatch and version. The hook path comes first. |
| `internal/hooks` | Hook input parser and event normalization. |
| `internal/state` | Instance model, pure reducer, SQLite store, and reconciliation. |
| `internal/process` | Owner discovery and liveness with `sysctl`. |
| `internal/terminal` | Terminal detection and the iTerm2 adapter. |
| `internal/transcript` | Bounded transcript tail reader and incremental token usage reader. |
| `internal/textutil` | Text sanitation and truncation. |
| `internal/gitinfo` | Branch name from `.git/HEAD`, without `git`. |
| `internal/setup` | Settings editor that keeps key order. |
| `internal/app` | Command logic with injected adapters. |
| `internal/tui` | Bubble Tea dashboard. |
| `scripts/ansi2png.py` | Converts rendered ANSI output to a PNG for the README images. |
