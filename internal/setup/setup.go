// Package setup installs and removes the hook entries of this tool in a
// Claude Code settings file. It changes no other settings.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cwatch/internal/hooks"
)

// ManagedArg marks a hook entry that this tool owns.
const ManagedArg = "--managed-by=cwatch"

// HookTimeout is the per-hook timeout, in seconds, for events other than
// SessionEnd. SessionEnd keeps the default budget of Claude Code so that
// this tool never delays the exit of Claude Code.
const HookTimeout = 5

// Config describes the hook command.
type Config struct {
	Exe        string // absolute executable path
	StateDir   string // absolute state directory
	NoExcerpts bool
}

// Args returns the argument vector of the hook command.
func (c Config) Args() []string {
	args := []string{"hook", ManagedArg, "--state-dir", c.StateDir}
	if c.NoExcerpts {
		args = append(args, "--no-excerpts")
	}
	return args
}

func (c Config) handler(event string) *object {
	h := newObject()
	h.set("type", "command")
	h.set("command", c.Exe)
	args := []any{}
	for _, a := range c.Args() {
		args = append(args, a)
	}
	h.set("args", args)
	if event != "SessionEnd" {
		h.set("timeout", json.Number(fmt.Sprint(HookTimeout)))
	}
	return h
}

// Validate checks that the configuration is safe to register.
func (c Config) Validate() error {
	if !filepath.IsAbs(c.Exe) {
		return fmt.Errorf("executable path %q is not absolute", c.Exe)
	}
	if !filepath.IsAbs(c.StateDir) {
		return fmt.Errorf("state directory %q is not absolute", c.StateDir)
	}
	if IsTemporaryBuild(c.Exe) {
		return fmt.Errorf("executable %q is a temporary build (for example from \"go run\"); build and install cwatch first", c.Exe)
	}
	return nil
}

// IsTemporaryBuild reports whether exe is in a temporary build directory.
func IsTemporaryBuild(exe string) bool {
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return true
	}
	for _, dir := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"} {
		if dir == "" {
			continue
		}
		if rel, err := filepath.Rel(dir, exe); err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// IsOwned reports whether a hook handler belongs to this tool. It matches
// the marker argument, and it also matches an earlier entry whose
// executable is named cwatch and whose first argument is "hook". It
// matches entries with any executable path.
func IsOwned(h any) bool {
	o, ok := h.(*object)
	if !ok {
		return false
	}
	if t, _ := o.get("type"); t != "command" {
		return false
	}
	rawArgs, _ := o.get("args")
	args, _ := rawArgs.([]any)
	for _, a := range args {
		if a == ManagedArg {
			return true
		}
	}
	cmd, _ := o.get("command")
	cs, _ := cmd.(string)
	if filepath.Base(cs) == "cwatch" && len(args) > 0 && args[0] == "hook" {
		return true
	}
	return false
}

// Change describes one edit.
type Change struct {
	Event  string
	Action string // "add", "replace", "remove", "unchanged"
}

func hooksObject(root *object, create bool) (*object, error) {
	v, ok := root.get("hooks")
	if !ok {
		if !create {
			return nil, nil
		}
		h := newObject()
		root.set("hooks", h)
		return h, nil
	}
	h, ok := v.(*object)
	if !ok {
		return nil, errors.New(`the "hooks" setting is not a JSON object`)
	}
	return h, nil
}

// removeOwned removes owned handlers from one event list. It removes a
// matcher group only when the group held only owned handlers.
func removeOwned(list []any) ([]any, int, error) {
	var out []any
	removed := 0
	for _, g := range list {
		group, ok := g.(*object)
		if !ok {
			out = append(out, g)
			continue
		}
		hv, ok := group.get("hooks")
		handlers, isArr := hv.([]any)
		if !ok || !isArr {
			out = append(out, g)
			continue
		}
		var keep []any
		for _, h := range handlers {
			if IsOwned(h) {
				removed++
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) == 0 && len(handlers) > 0 {
			continue
		}
		if len(keep) != len(handlers) {
			if keep == nil {
				keep = []any{}
			}
			group.set("hooks", keep)
		}
		out = append(out, g)
	}
	if out == nil {
		out = []any{}
	}
	return out, removed, nil
}

func ownedHandlers(list []any) []*object {
	var out []*object
	for _, g := range list {
		group, ok := g.(*object)
		if !ok {
			continue
		}
		hv, _ := group.get("hooks")
		handlers, _ := hv.([]any)
		for _, h := range handlers {
			if IsOwned(h) {
				out = append(out, h.(*object))
			}
		}
	}
	return out
}

func equalJSON(a, b any) bool {
	ea, err1 := encodeJSON(a)
	eb, err2 := encodeJSON(b)
	return err1 == nil && err2 == nil && bytes.Equal(ea, eb)
}

func parseSettings(data []byte) (*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return newObject(), nil
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("settings file is not valid JSON: %w", err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, errors.New("settings file does not hold a JSON object")
	}
	return root, nil
}

// PlanInstall computes the settings after installation. It returns the new
// content, the changes, and whether the content changed.
func PlanInstall(data []byte, c Config) ([]byte, []Change, bool, error) {
	root, err := parseSettings(data)
	if err != nil {
		return nil, nil, false, err
	}
	h, err := hooksObject(root, true)
	if err != nil {
		return nil, nil, false, err
	}
	var changes []Change
	modified := false
	for _, ev := range hooks.Events {
		want := c.handler(ev)
		var list []any
		if v, ok := h.get(ev); ok {
			arr, ok := v.([]any)
			if !ok {
				return nil, nil, false, fmt.Errorf("the hooks.%s setting is not a JSON array", ev)
			}
			list = arr
		}
		owned := ownedHandlers(list)
		if len(owned) == 1 && equalJSON(owned[0], want) {
			changes = append(changes, Change{Event: ev, Action: "unchanged"})
			continue
		}
		action := "add"
		if len(owned) > 0 {
			action = "replace"
		}
		list, _, _ = removeOwned(list)
		group := newObject()
		group.set("hooks", []any{want})
		list = append(list, group)
		h.set(ev, list)
		modified = true
		changes = append(changes, Change{Event: ev, Action: action})
	}
	out, err := encodeJSON(root)
	return out, changes, modified, err
}

// PlanUninstall computes the settings after removal of the owned handlers.
func PlanUninstall(data []byte) ([]byte, []Change, bool, error) {
	root, err := parseSettings(data)
	if err != nil {
		return nil, nil, false, err
	}
	h, err := hooksObject(root, false)
	if err != nil || h == nil {
		out, _ := encodeJSON(root)
		return out, nil, false, err
	}
	var changes []Change
	modified := false
	events := append([]string(nil), h.keys...)
	sort.Strings(events)
	for _, ev := range events {
		v, _ := h.get(ev)
		list, ok := v.([]any)
		if !ok {
			continue
		}
		newList, removed, _ := removeOwned(list)
		if removed == 0 {
			continue
		}
		modified = true
		changes = append(changes, Change{Event: ev, Action: "remove"})
		if len(newList) == 0 {
			h.del(ev)
		} else {
			h.set(ev, newList)
		}
	}
	if modified && len(h.keys) == 0 {
		root.del("hooks")
	}
	out, err := encodeJSON(root)
	return out, changes, modified, err
}

// Installed reports the events that have an owned handler and the
// executable paths that those handlers use.
func Installed(data []byte) (map[string][]string, error) {
	root, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	h, err := hooksObject(root, false)
	if err != nil || h == nil {
		return map[string][]string{}, err
	}
	out := map[string][]string{}
	for _, ev := range h.keys {
		v, _ := h.get(ev)
		list, _ := v.([]any)
		for _, o := range ownedHandlers(list) {
			cmd, _ := o.get("command")
			s, _ := cmd.(string)
			out[ev] = append(out[ev], s)
		}
	}
	return out, nil
}

// StateDirs returns the state directories that the owned handlers use.
func StateDirs(data []byte) []string {
	root, err := parseSettings(data)
	if err != nil {
		return nil
	}
	h, err := hooksObject(root, false)
	if err != nil || h == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ev := range h.keys {
		v, _ := h.get(ev)
		list, _ := v.([]any)
		for _, o := range ownedHandlers(list) {
			rawArgs, _ := o.get("args")
			args, _ := rawArgs.([]any)
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "--state-dir" {
					if d, ok := args[i+1].(string); ok && !seen[d] {
						seen[d] = true
						out = append(out, d)
					}
				}
			}
		}
	}
	return out
}

// ReadSettings reads the settings file. A missing file reads as empty.
func ReadSettings(path string) ([]byte, os.FileInfo, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	fi, err := os.Stat(path)
	return data, fi, err
}

// ResolveTarget follows symbolic links so that the write replaces the real
// file and keeps the link.
func ResolveTarget(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// ErrConcurrentEdit means that the settings file changed during the edit.
var ErrConcurrentEdit = errors.New("the settings file changed while cwatch edited it; run the command again")

// WriteSettings replaces path with data. It writes a backup of the original
// content first. It checks that the file still holds original, then renames
// a temporary file over it. It keeps the file mode.
func WriteSettings(path string, original []byte, origInfo os.FileInfo, data []byte, now time.Time) (backup string, err error) {
	path = ResolveTarget(path)
	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if origInfo != nil {
		mode = origInfo.Mode().Perm()
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if origInfo != nil {
		backup = fmt.Sprintf("%s.cwatch-backup-%s", path, now.UTC().Format("20060102T150405.000000000Z"))
		if err := writeFileSync(backup, original, mode, true); err != nil {
			return "", fmt.Errorf("write backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".cwatch-*")
	if err != nil {
		return backup, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Close(); err != nil {
		return backup, err
	}
	// Detect a concurrent edit just before the rename.
	current, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if origInfo != nil {
			return backup, ErrConcurrentEdit
		}
	case err != nil:
		return backup, err
	case origInfo == nil || !bytes.Equal(current, original):
		return backup, ErrConcurrentEdit
	}
	if err := os.Rename(tmpName, path); err != nil {
		return backup, err
	}
	return backup, nil
}

func writeFileSync(path string, data []byte, mode os.FileMode, exclusive bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
