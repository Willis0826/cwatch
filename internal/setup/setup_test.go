package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const existing = `{
  "model": "opus",
  "env": {"FOO": "bar"},
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "say done"}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/guard"}]}
    ]
  },
  "permissions": {"allow": ["Bash(ls:*)"]},
  "zeta": 1.50
}
`

func cfg(t *testing.T) Config {
	dir := filepath.Join("/Users/o'neil/My Tools", "bin")
	return Config{Exe: filepath.Join(dir, "cwatch"), StateDir: "/Users/o'neil/.cwatch"}
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return m
}

// Acceptance test 9.
func TestInstallIdempotentAndUninstallPreserves(t *testing.T) {
	c := cfg(t)
	once, changes, modified, err := PlanInstall([]byte(existing), c)
	if err != nil || !modified {
		t.Fatalf("install: %v %v", modified, err)
	}
	if len(changes) != 10 {
		t.Fatalf("%d changes", len(changes))
	}
	twice, changes2, modified2, err := PlanInstall(once, c)
	if err != nil || modified2 || string(twice) != string(once) {
		t.Fatalf("second install modified the settings: %v %v", modified2, err)
	}
	for _, ch := range changes2 {
		if ch.Action != "unchanged" {
			t.Fatalf("second install: %+v", ch)
		}
	}
	m := decode(t, once)
	stop := m["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop has %d groups, want 2", len(stop))
	}
	h := stop[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if h["command"] != c.Exe {
		t.Fatalf("command %v", h["command"])
	}
	args := h["args"].([]any)
	if args[0] != "hook" || args[3] != "/Users/o'neil/.cwatch" {
		t.Fatalf("args %v", args)
	}
	end := m["hooks"].(map[string]any)["SessionEnd"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if _, ok := end["timeout"]; ok {
		t.Fatal("SessionEnd hook has a timeout; it must keep the default budget")
	}
	// Key order and number formatting stay the same.
	if !strings.HasPrefix(string(once), "{\n  \"model\": \"opus\",\n  \"env\"") || !strings.Contains(string(once), `"zeta": 1.50`) {
		t.Fatalf("order or numbers changed:\n%s", once)
	}

	removed, _, modified3, err := PlanUninstall(once)
	if err != nil || !modified3 {
		t.Fatalf("uninstall: %v %v", modified3, err)
	}
	if !equalJSONText(t, removed, []byte(existing)) {
		t.Fatalf("uninstall did not restore the settings:\n%s", removed)
	}
}

func equalJSONText(t *testing.T, a, b []byte) bool {
	ea, _ := json.Marshal(decode(t, a))
	eb, _ := json.Marshal(decode(t, b))
	return string(ea) == string(eb)
}

func TestInstallReplacesMovedExecutable(t *testing.T) {
	c := cfg(t)
	once, _, _, _ := PlanInstall([]byte(existing), c)
	c2 := c
	c2.Exe = "/opt/cwatch/bin/cwatch"
	moved, changes, modified, err := PlanInstall(once, c2)
	if err != nil || !modified || changes[0].Action != "replace" {
		t.Fatalf("replace: %v %v %+v", modified, err, changes)
	}
	inst, _ := Installed(moved)
	for ev, paths := range inst {
		if len(paths) != 1 || paths[0] != c2.Exe {
			t.Fatalf("%s: %v", ev, paths)
		}
	}
}

func TestUninstallRemovesOnlyOwned(t *testing.T) {
	// A group that mixes an owned handler and a user handler keeps the user
	// handler.
	mixed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say hi"},{"type":"command","command":"/old/path/cwatch","args":["hook","--state-dir","/x"]}]}],"Other":[]}}`
	out, _, modified, err := PlanUninstall([]byte(mixed))
	if err != nil || !modified {
		t.Fatalf("%v %v", modified, err)
	}
	m := decode(t, out)
	stop := m["hooks"].(map[string]any)["Stop"].([]any)
	hs := stop[0].(map[string]any)["hooks"].([]any)
	if len(hs) != 1 || hs[0].(map[string]any)["command"] != "say hi" {
		t.Fatalf("hooks %v", hs)
	}
	if _, ok := m["hooks"].(map[string]any)["Other"]; !ok {
		t.Fatal("an unrelated empty event list was removed")
	}
}

func TestMalformedSettingsFail(t *testing.T) {
	for _, bad := range []string{`{"hooks": `, `[1,2]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`, `{"a":1,"a":2}`} {
		if _, _, _, err := PlanInstall([]byte(bad), cfg(t)); err == nil {
			t.Errorf("PlanInstall(%q) returned no error", bad)
		}
	}
}

func TestWriteSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude config's dir")
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(existing), 0o640)
	data, info, err := ReadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, _ := PlanInstall(data, cfg(t))
	backup, err := WriteSettings(path, data, info, out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != existing {
		t.Fatal("backup differs from the original")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %o not kept", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); string(got) != string(out) {
		t.Fatal("new content not written")
	}
	// A concurrent edit between read and write is detected.
	data2, info2, _ := ReadSettings(path)
	os.WriteFile(path, []byte(`{"edited":true}`), 0o640)
	if _, err := WriteSettings(path, data2, info2, []byte("{}\n"), time.Now()); !errors.Is(err, ErrConcurrentEdit) {
		t.Fatalf("concurrent edit: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"edited":true}` {
		t.Fatal("concurrent edit was overwritten")
	}
}

func TestWriteSettingsKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "settings.json")
	os.WriteFile(real, []byte(`{}`), 0o600)
	os.Symlink(real, link)
	data, info, _ := ReadSettings(link)
	out, _, _, _ := PlanInstall(data, cfg(t))
	if _, err := WriteSettings(link, data, info, out, time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symbolic link replaced")
	}
	if got, _ := os.ReadFile(real); string(got) != string(out) {
		t.Fatal("target not updated")
	}
}

func TestValidateRejectsTemporaryBuild(t *testing.T) {
	c := Config{Exe: filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "cwatch"), StateDir: "/x"}
	if err := c.Validate(); err == nil {
		t.Fatal("temporary build accepted")
	}
	if err := (Config{Exe: "cwatch", StateDir: "/x"}).Validate(); err == nil {
		t.Fatal("relative path accepted")
	}
}
