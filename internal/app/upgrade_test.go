package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeReleases struct {
	tag   string
	files map[string][]byte
	err   error
}

func (f *fakeReleases) Latest(context.Context) (string, error) { return f.tag, f.err }

func (f *fakeReleases) Download(_ context.Context, tag, name, dst string) error {
	b, ok := f.files[name]
	if !ok || tag != f.tag {
		return fmt.Errorf("404 %s/%s", tag, name)
	}
	return os.WriteFile(dst, b, 0o600)
}

func archiveWith(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// release returns a fake release whose binary is a script that prints
// reported for "cwatch version".
func release(t *testing.T, tag, reported string) *fakeReleases {
	t.Helper()
	name := "cwatch-darwin-" + runtime.GOARCH + ".tar.gz"
	arc := archiveWith(t, "cwatch", []byte("#!/bin/sh\necho '"+reported+"'\n"))
	sum := sha256.Sum256(arc)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n" + strings.Repeat("0", 64) + "  other.tar.gz\n"
	return &fakeReleases{tag: tag, files: map[string][]byte{name: arc, "SHA256SUMS": []byte(sums)}}
}

func upgradeEnv(t *testing.T, rel Releases) (*Env, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("releases exist only for macOS")
	}
	exe := filepath.Join(t.TempDir(), "cwatch")
	writeFile(t, exe, []byte("old"))
	var out, errb bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errb, Releases: rel, Executable: func() (string, error) { return exe, nil }}
	return env, exe, &out, &errb
}

func TestUpgradeReplacesBinary(t *testing.T) {
	env, exe, out, errb := upgradeEnv(t, release(t, "v0.3.0", "cwatch 0.3.0 (abc1234) darwin/arm64"))
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.2.0"}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	b := readFile(t, exe)
	if !strings.Contains(string(b), "cwatch 0.3.0") {
		t.Fatalf("binary not replaced: %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if !strings.Contains(out.String(), "Upgraded cwatch from 0.2.0 to 0.3.0") {
		t.Fatalf("output %q", out)
	}
	// No temporary file stays next to the binary.
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Fatalf("files %v", entries)
	}
}

func TestUpgradeUpToDateAndCheck(t *testing.T) {
	env, exe, out, _ := upgradeEnv(t, release(t, "v0.2.0", "cwatch 0.2.0"))
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.2.0"}); code != ExitOK || !strings.Contains(out.String(), "is the latest version") {
		t.Fatalf("exit %d: %q", code, out)
	}
	out.Reset()
	env.Releases = release(t, "v0.3.0", "cwatch 0.3.0")
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.2.0", Check: true}); code != ExitOK || !strings.Contains(out.String(), "Latest version:  0.3.0") || !strings.Contains(out.String(), "Run \"cwatch upgrade\"") {
		t.Fatalf("check: exit %d: %q", code, out)
	}
	out.Reset()
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.4.0"}); code != ExitOK || !strings.Contains(out.String(), "newer than the latest release") {
		t.Fatalf("newer: exit %d: %q", code, out)
	}
	if string(readFile(t, exe)) != "old" {
		t.Fatal("the binary changed")
	}
}

func TestUpgradeDevBuildNeedsForce(t *testing.T) {
	env, exe, _, errb := upgradeEnv(t, release(t, "v0.3.0", "cwatch 0.3.0"))
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "dev"}); code != ExitError || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("exit %d: %q", code, errb)
	}
	if string(readFile(t, exe)) != "old" {
		t.Fatal("the binary changed without --force")
	}
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "dev", Force: true}); code != ExitOK {
		t.Fatalf("force: exit %d: %q", code, errb)
	}
}

func TestUpgradeRejectsBadDownloads(t *testing.T) {
	name := "cwatch-darwin-" + runtime.GOARCH + ".tar.gz"
	cases := map[string]func(*fakeReleases){
		"hash mismatch": func(f *fakeReleases) { f.files[name] = append(f.files[name], 0) },
		"no checksum":   func(f *fakeReleases) { f.files["SHA256SUMS"] = []byte("") },
		"wrong version": func(f *fakeReleases) {
			*f = *release(t, "v0.3.0", "cwatch 0.2.9")
		},
		"no binary": func(f *fakeReleases) {
			arc := archiveWith(t, "other", []byte("x"))
			sum := sha256.Sum256(arc)
			f.files[name] = arc
			f.files["SHA256SUMS"] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
		},
		"latest fails": func(f *fakeReleases) { f.err = errors.New("offline") },
	}
	for label, change := range cases {
		rel := release(t, "v0.3.0", "cwatch 0.3.0")
		change(rel)
		env, exe, _, errb := upgradeEnv(t, rel)
		if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.2.0"}); code != ExitError {
			t.Errorf("%s: exit %d", label, code)
		}
		if string(readFile(t, exe)) != "old" {
			t.Errorf("%s: the binary changed: %s", label, errb)
		}
		if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
			t.Errorf("%s: files %v", label, entries)
		}
	}
}

func TestUpgradeUnwritableDirectory(t *testing.T) {
	env, exe, _, errb := upgradeEnv(t, release(t, "v0.3.0", "cwatch 0.3.0"))
	dir := filepath.Dir(exe)
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)
	if code := env.Upgrade(context.Background(), UpgradeOptions{Current: "0.2.0"}); code != ExitError || !strings.Contains(errb.String(), "sudo "+exe+" upgrade") {
		t.Fatalf("exit %d: %q", code, errb)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.10.0", -1}, {"1.0.0", "0.9.9", 1}, {"0.2.0", "v0.2.0", 0},
		{"0.2.0-rc.1", "0.2.0", -1}, {"0.2.0-rc.2", "0.2.0-rc.1", 1},
	} {
		a, _ := parseVersion(c.a)
		b, _ := parseVersion(c.b)
		if got := compareVersions(a, b); got != c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"dev", "1.2", "1.x.3", ""} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("parseVersion(%q) accepted", bad)
		}
	}
}
