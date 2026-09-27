# Contributing to cwatch

Thank you for your help. This guide tells you how to report a problem, how to propose a change, and which rules a change must follow.

All contributors must obey the [Code of Conduct](CODE_OF_CONDUCT.md).

## Report a problem

Open an issue on GitHub. Include these items:

- The output of `cwatch version`.
- The output of `cwatch doctor`.
- Your macOS version, your iTerm2 version, and your Claude Code version (`claude --version`).
- The steps that cause the problem, the result that you expected, and the result that you got.

CAUTION: `cwatch list` and the details view can show your prompts and file paths. Remove private text before you paste output into an issue.

## Report a security problem

Do not open a public issue for a security problem. Report it privately on the **Security** tab of the repository, with **Report a vulnerability** (https://github.com/Willis0826/cwatch/security/advisories/new). Tell what an attacker can do and which version you tested.

Only you and the maintainers of the repository can see the report.

## Propose a change

For a small fix, open a pull request directly. For a new command, a new dependency, or a change in the behaviour of the hook, open an issue first. Then we can agree on the design before you write the code.

1. Fork the repository and make a branch from `main`.
2. Make the change. Add or change the tests for it.
3. Run the checks (see [Checks](#checks)).
4. Open a pull request. Tell what the change does, why it is necessary, and how you tested it.

## Set up

You need macOS, Go 1.26 or later (`brew install go`), and iTerm2 to test the focus function.

```sh
git clone https://github.com/<you>/cwatch.git
cd cwatch
make build        # writes bin/cwatch
```

To try your build with real sessions, run `make setup`. This command installs your build in place of the installed binary and points the hooks at it. To go back to the release, run `cwatch upgrade --force`.

[DEVELOP.md](DEVELOP.md) explains the design, the commands, the storage, and the project layout. Read it before you make a large change.

## Checks

Run these commands before you open a pull request. The release workflow runs the same checks.

```sh
gofmt -l .        # must print nothing
make vet
make race         # go test -race ./...
```

## Rules for code

- **The hook must never block Claude Code.** `cwatch hook` always exits with code 0, writes nothing to stdout, and stops after 3 seconds. Do not add work to the hook path that can be slow or can fail. Do the work in `list` or in the dashboard instead.
- **No HTTP client in the binary.** `TestNoNetworkClientDependencies` makes sure that the binary does not link `net/http` or `crypto/tls`. Only `cwatch summary` and `cwatch upgrade` use the network, through the `claude` and `curl` programs.
- **No model calls outside `cwatch summary`.** The dashboard and `list` read local data only.
- **Treat transcripts and hook input as data.** Pass all text through `textutil.Sanitize` before you store it or show it. Keep the size limits on each read.
- **Store as little as possible.** Do not store tool input, tool output, or the raw hook payload. Tell about all new stored data in the "Storage and privacy" section of `DEVELOP.md`.
- **Inject the adapters.** Code in `internal/app` gets the clock, the process inspector, the terminal, the model call, and the release download from `Env`. Tests replace them. A test must not use the network, the real `~/.cwatch`, or the real `~/.claude`.
- **Keep the dependencies small.** Do not add a module without a reason in the pull request.

## Rules for text

Write the documentation, the code comments, the user-facing strings, and the commit messages in [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org/). Use British English spelling, for example "summarise" and "behaviour". Code identifiers keep their names.

The most important STE rules:

- Write one instruction in each sentence.
- Use the active voice and the imperative, for example "Run the command", not "The command should be run".
- Keep procedural sentences to 20 words or fewer, and descriptive sentences to 25 words or fewer.
- Use one word for one thing. Do not change words for variety.
- Put a warning before the step that it applies to, and start it with a clear command.

When you change a command, a key, or a stored file, update `README.md` and `DEVELOP.md` in the same pull request.

## Screenshots

If you change the dashboard, make the README images again:

```sh
make screenshots
```

This target needs Go, Python 3, and Google Chrome. It renders the dashboard with the demo data in `internal/tui/demo_test.go`. Do not put real session data in the images.

## Commit messages

- Write the subject in the imperative, for example "Add the upgrade command". Do not end it with a full stop.
- In the body, tell what changed and why.
- Make one commit for one change, if possible.

## Releases

Only the maintainer makes releases. The maintainer changes `VERSION` in the `Makefile`, then pushes a version tag such as `v0.4.0`. The release workflow builds, tests, and publishes the binaries. For more information, see the "Release" section of [DEVELOP.md](DEVELOP.md#release).

## Licence

cwatch uses the [MIT License](LICENSE). When you send a contribution, you agree that it uses the same licence.
