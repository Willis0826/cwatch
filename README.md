# cwatch

**See all your Claude Code sessions in one place.**

`cwatch` is a dashboard for macOS and iTerm2. It shows each Claude Code session in your windows, tabs, and split panes. You can see at a glance which sessions need you, which work, and which are idle. Push `Enter` to go to the pane of a session.

![The cwatch dashboard](docs/images/dashboard.png)

- 🟡 **permission**: Claude waits for you to approve a tool.
- 🟢 **working**: Claude works on your prompt.
- ⚪ **idle**: Claude finished and waits for your next prompt.
- 🔴 **error**: the turn stopped because of an API error.

The table also shows the project, the git branch, the current tool or the latest message, and the context size in tokens. Push `→` to see the details of a session:

![The details view](docs/images/details.png)

cwatch runs only on your computer. It makes no model calls and uses no network. Its data stays in `~/.cwatch`.

## Install

You need macOS, iTerm2, Claude Code, and Go 1.26 or later. To install Go, run `brew install go`.

1. In the project directory, run:

   ```sh
   make setup
   ```

   This command installs `cwatch`, shows the change to your Claude Code settings, and asks you to confirm. Then it adds the hooks and checks the result.

2. Restart the Claude Code sessions that already run, then submit a prompt in each.

3. Open the dashboard:

   ```sh
   cwatch
   ```

   | Key | Function |
   |---|---|
   | `↑` `↓` | Select a session. |
   | `Enter` | Go to the pane of the session. |
   | `→` / `←` | Open or close the details. |
   | `/` | Filter. |
   | `q` | Quit. |

The first time that you push `Enter`, macOS can ask for permission to control iTerm2. Allow it.

## Uninstall

1. In the project directory, run:

   ```sh
   make uninstall
   ```

   This command removes the cwatch hooks from your Claude Code settings and deletes the `cwatch` program. Your other settings and hooks do not change.

2. Restart the Claude Code sessions that run.

3. Optional: delete the history.

   ```sh
   rm -rf ~/.cwatch
   ```

---

For the build steps, the commands, and the design, see [DEVELOP.md](DEVELOP.md).
