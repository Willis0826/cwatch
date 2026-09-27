// Command cwatch shows the Claude Code sessions in iTerm2 panes and focuses
// the pane of a selected session.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"cwatch/internal/app"
	"cwatch/internal/process"
	"cwatch/internal/summary"
	"cwatch/internal/tui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `cwatch shows Claude Code sessions in iTerm2 panes and focuses them.

Usage:
  cwatch [flags]                   Open the dashboard (no implicit setup)
  cwatch list [--json] [--all]     List tracked instances
  cwatch focus <instance-id>       Focus the iTerm2 pane of an instance
  cwatch summary yesterday|week [--refresh]
                                   Summarise your work of yesterday or last week
                                   with "claude -p"
  cwatch upgrade [--check] [--force]
                                   Install the latest release in place of this binary
  cwatch setup [--dry-run]         Install the cwatch hooks
  cwatch uninstall [--dry-run]     Remove the cwatch hooks; keep the history
  cwatch doctor                    Check configuration and tracking
  cwatch hook                      Internal: read one Claude Code hook event from stdin
  cwatch version                   Show the version

Flags for all commands:
  --state-dir DIR       State directory (default ~/.cwatch)
  --settings-file FILE  Claude Code settings file (default ~/.claude/settings.json,
                        or $CLAUDE_CONFIG_DIR/settings.json)
  --no-excerpts         Do not show message excerpts. With setup: also do not store them.
`

func main() {
	// The hook path is first. It does no flag parsing, no UI setup, and no
	// scan of the stored sessions.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(app.RunHook(os.Args[2:], os.Stdin, os.Stderr, os.Getenv, process.System{}))
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// splitCommand finds the subcommand. Global flags can come before it.
func splitCommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--state-dir" || a == "--settings-file" || a == "-state-dir" || a == "-settings-file" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		rest := append(append([]string{}, args[:i]...), args[i+1:]...)
		return a, rest
	}
	return "", args
}

// parseInterspersed parses flags that come before or after the arguments,
// as in "cwatch summary week --refresh". After the call, fs.Args holds the
// arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	return fs.Parse(append([]string{"--"}, pos...))
}

type common struct {
	stateDir     string
	settingsFile string
	noExcerpts   bool
}

func newFlags(name string, stderr io.Writer, c *common) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.stateDir, "state-dir", "", "state directory")
	fs.StringVar(&c.settingsFile, "settings-file", "", "Claude Code settings file")
	fs.BoolVar(&c.noExcerpts, "no-excerpts", false, "do not show or store message excerpts")
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	return fs
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, rest := splitCommand(args)
	var c common
	fs := newFlags(cmd, stderr, &c)
	var jsonOut, all, dryRun, refresh, check, force bool
	switch cmd {
	case "list":
		fs.BoolVar(&jsonOut, "json", false, "JSON output")
		fs.BoolVar(&all, "all", false, "include ended instances")
	case "", "dashboard":
		fs.BoolVar(&all, "all", false, "include ended instances")
	case "summary":
		fs.BoolVar(&refresh, "refresh", false, "make the summary again")
	case "upgrade":
		fs.BoolVar(&check, "check", false, "only compare the versions")
		fs.BoolVar(&force, "force", false, "replace a development build or the same version")
	case "setup", "uninstall":
		fs.BoolVar(&dryRun, "dry-run", false, "show the changes only")
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return app.ExitOK
	case "version":
		fmt.Fprintln(stdout, versionString())
		return app.ExitOK
	case "focus", "doctor":
	default:
		fmt.Fprintf(stderr, "cwatch: unknown command %q\n\n%s", cmd, usage)
		return app.ExitUsage
	}
	if err := parseInterspersed(fs, rest); err != nil {
		if err == flag.ErrHelp {
			return app.ExitOK
		}
		return app.ExitUsage
	}

	env := app.DefaultEnv()
	env.Stdout, env.Stderr = stdout, stderr
	env.StateDir, env.SettingsFile = c.stateDir, c.settingsFile
	if err := env.Resolve(); err != nil {
		fmt.Fprintln(stderr, "cwatch:", err)
		return app.ExitError
	}
	ctx := context.Background()

	switch cmd {
	case "", "dashboard":
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "cwatch: unexpected argument %q\n", fs.Arg(0))
			return app.ExitUsage
		}
		// A hook entry without its arguments must not start the dashboard.
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintln(stderr, "cwatch: the dashboard needs a terminal; use \"cwatch list\" for plain output")
			return app.ExitError
		}
		if err := tui.Run(env, tui.Options{Excerpts: !c.noExcerpts, All: all}); err != nil {
			fmt.Fprintln(stderr, "cwatch:", err)
			return app.ExitError
		}
		return app.ExitOK

	case "list":
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		snap := env.Load(ctx, app.LoadOptions{All: all, Prune: true})
		if jsonOut {
			if err := app.WriteJSON(stdout, snap, env.Now(), !c.noExcerpts); err != nil {
				fmt.Fprintln(stderr, "cwatch:", err)
				return app.ExitError
			}
		} else if len(snap.Instances) == 0 {
			fmt.Fprintln(stdout, app.StatusMessage(snap.Status, snap.Err))
		} else {
			width := 120
			if f, ok := stdout.(*os.File); ok {
				if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
					width = w
				}
			}
			app.WriteTable(stdout, snap.Instances, env.Now(), width, !c.noExcerpts)
		}
		if snap.Status == app.StatusUnavailable {
			return app.ExitError
		}
		return app.ExitOK

	case "focus":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "cwatch: focus needs one instance ID (see \"cwatch list\")")
			return app.ExitUsage
		}
		return env.FocusCommand(ctx, fs.Arg(0))

	case "summary":
		return summaryCommand(ctx, env, fs.Args(), refresh)

	case "upgrade":
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "cwatch: unexpected argument %q\n", fs.Arg(0))
			return app.ExitUsage
		}
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return env.Upgrade(ctx, app.UpgradeOptions{Current: version, Check: check, Force: force})

	case "setup":
		return env.Setup(app.SetupOptions{DryRun: dryRun, NoExcerpts: c.noExcerpts})

	case "uninstall":
		return env.Uninstall(dryRun)

	case "doctor":
		return env.Doctor(ctx)
	}
	return app.ExitUsage
}

func summaryCommand(ctx context.Context, env *app.Env, args []string, refresh bool) int {
	if len(args) != 1 {
		fmt.Fprintf(env.Stderr, "cwatch: summary needs one range: %q or %q\n", summary.KindYesterday, summary.KindWeek)
		return app.ExitUsage
	}
	r, err := summary.Parse(args[0], env.Now())
	if err != nil {
		fmt.Fprintln(env.Stderr, "cwatch:", err)
		return app.ExitUsage
	}
	if !refresh {
		if text, ok := env.CachedSummary(r); ok {
			fmt.Fprint(env.Stdout, text)
			return app.ExitOK
		}
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(env.Stderr, "Summarising %s…\n", r.Label())
	text, _, err := env.Summary(ctx, r, true)
	switch {
	case errors.Is(err, app.ErrNoActivity):
		fmt.Fprintf(env.Stdout, "No Claude Code activity in %s.\n", r.Label())
		return app.ExitOK
	case err != nil:
		fmt.Fprintln(env.Stderr, "cwatch:", err)
		return app.ExitError
	}
	fmt.Fprint(env.Stdout, text)
	return app.ExitOK
}

func versionString() string {
	v := version
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				v += " (" + s.Value[:7] + ")"
			}
		}
	}
	return fmt.Sprintf("cwatch %s %s/%s", v, runtime.GOOS, runtime.GOARCH)
}
