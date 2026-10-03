// Package launchagent writes and loads the LaunchAgent that starts the
// cwatch menu bar when the user logs in.
package launchagent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Label is the launchd label of the menu bar agent. The plist file name is
// the label plus ".plist".
const Label = "io.github.willis0826.cwatch.menubar"

// Config holds the values in the plist.
type Config struct {
	Exe          string
	StateDir     string
	SettingsFile string
	LogFile      string
}

// Args returns the arguments after the executable.
func (c Config) Args() []string {
	return []string{"menubar", "--state-dir", c.StateDir, "--settings-file", c.SettingsFile}
}

// Path returns the plist path in dir, which is normally
// ~/Library/LaunchAgents.
func Path(dir string) string { return filepath.Join(dir, Label+".plist") }

// DefaultDir returns ~/Library/LaunchAgents.
func DefaultDir(home string) string { return filepath.Join(home, "Library", "LaunchAgents") }

// Plist returns the plist content. The agent starts at login. launchd
// starts it again after a crash, but not after the user quits it.
func (c Config) Plist() []byte {
	var b bytes.Buffer
	str := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return "<string>" + e.String() + "</string>"
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + str(Label) + `
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{c.Exe}, c.Args()...) {
		b.WriteString("\t\t" + str(a) + "\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	` + str(c.LogFile) + `
	<key>StandardErrorPath</key>
	` + str(c.LogFile) + `
</dict>
</plist>
`)
	return b.Bytes()
}

// Launchctl runs launchctl. Tests use a fake.
type Launchctl interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// System runs /bin/launchctl.
type System struct{}

// Run runs launchctl with args and returns the combined output.
func (System) Run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	return string(out), err
}

// Domain returns the GUI domain of the current user.
func Domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func target() string { return Domain() + "/" + Label }

// Load stops the agent if it runs, then loads the plist at path. launchd
// can refuse a bootstrap just after a bootout, so Load tries again.
func Load(ctx context.Context, lc Launchctl, path string) error {
	_, _ = lc.Run(ctx, "bootout", target())
	var out string
	var err error
	for i := 0; i < 10; i++ {
		if out, err = lc.Run(ctx, "bootstrap", Domain(), path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(out))
}

// Unload stops the agent and removes it from launchd. An agent that is not
// loaded is not an error.
func Unload(ctx context.Context, lc Launchctl) {
	_, _ = lc.Run(ctx, "bootout", target())
}

// Restart stops the running agent and starts it again, for example after
// an upgrade replaced the binary.
func Restart(ctx context.Context, lc Launchctl) error {
	out, err := lc.Run(ctx, "kickstart", "-k", target())
	if err != nil {
		return fmt.Errorf("launchctl kickstart: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Running reports whether launchd runs the agent now.
func Running(ctx context.Context, lc Launchctl) bool {
	out, err := lc.Run(ctx, "print", target())
	return err == nil && strings.Contains(out, "state = running")
}

// Read returns the plist content at path, or nil when the file does not
// exist.
func Read(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Write writes the plist with mode 0644 through a temporary file.
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cwatch-plist-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
