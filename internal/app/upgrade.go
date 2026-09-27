package app

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Upgrade limits.
const (
	ReleaseRepo       = "Willis0826/cwatch"
	UpgradeTimeout    = 5 * time.Minute
	maxBinaryBytes    = 64 << 20
	maxChecksumsBytes = 64 << 10
)

// Releases finds and downloads cwatch releases.
type Releases interface {
	// Latest returns the tag of the latest release, for example "v0.2.0".
	Latest(ctx context.Context) (string, error)
	// Download writes the release asset name of tag to dst.
	Download(ctx context.Context, tag, name, dst string) error
}

// CurlReleases uses curl to get GitHub releases. cwatch itself links no
// HTTP client.
type CurlReleases struct{ Repo string }

// Latest follows the redirect of the "latest" release page.
func (c CurlReleases) Latest(ctx context.Context) (string, error) {
	out, err := c.curl(ctx, "-fsSLI", "-o", "/dev/null", "-w", "%{url_effective}",
		"https://github.com/"+c.Repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	i := strings.LastIndex(url, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("no release found at %s", url)
	}
	return url[i+len("/tag/"):], nil
}

// Download gets one asset of a tag.
func (c CurlReleases) Download(ctx context.Context, tag, name, dst string) error {
	_, err := c.curl(ctx, "-fsSL", "-o", dst,
		"https://github.com/"+c.Repo+"/releases/download/"+tag+"/"+name)
	return err
}

func (c CurlReleases) curl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "curl", append([]string{"--max-time", "120", "--proto", "=https"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("curl: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// UpgradeOptions are the arguments of the upgrade command.
type UpgradeOptions struct {
	Current string // the version of the running binary
	Check   bool   // only compare the versions
	Force   bool   // replace a development build or the same version
}

// Upgrade replaces the running binary with the latest release.
func (e *Env) Upgrade(ctx context.Context, o UpgradeOptions) int {
	ctx, cancel := context.WithTimeout(ctx, UpgradeTimeout)
	defer cancel()
	rel := e.Releases
	if rel == nil {
		rel = CurlReleases{Repo: ReleaseRepo}
	}
	tag, err := rel.Latest(ctx)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: cannot find the latest release:", err)
		return ExitError
	}
	latest := strings.TrimPrefix(tag, "v")
	if _, ok := parseVersion(latest); !ok {
		fmt.Fprintf(e.Stderr, "cwatch: the latest release has an unknown version %q\n", tag)
		return ExitError
	}
	cmp, devBuild := 0, false
	if cur, ok := parseVersion(o.Current); ok {
		lat, _ := parseVersion(latest)
		cmp = compareVersions(cur, lat)
	} else {
		devBuild = true
	}

	if o.Check {
		fmt.Fprintf(e.Stdout, "Current version: %s\nLatest version:  %s\n", o.Current, latest)
		switch {
		case devBuild:
			fmt.Fprintln(e.Stdout, "This is a development build. \"cwatch upgrade --force\" replaces it with the release.")
		case cmp < 0:
			fmt.Fprintln(e.Stdout, "Run \"cwatch upgrade\" to install the latest version.")
		default:
			fmt.Fprintln(e.Stdout, "cwatch is up to date.")
		}
		return ExitOK
	}
	switch {
	case devBuild && !o.Force:
		fmt.Fprintf(e.Stderr, "cwatch: this is a development build (%s). To replace it with release %s, run \"cwatch upgrade --force\".\n", o.Current, latest)
		return ExitError
	case cmp > 0 && !o.Force:
		fmt.Fprintf(e.Stdout, "cwatch %s is newer than the latest release %s. Nothing changed.\n", o.Current, latest)
		return ExitOK
	case cmp == 0 && !devBuild && !o.Force:
		fmt.Fprintf(e.Stdout, "cwatch %s is the latest version.\n", o.Current)
		return ExitOK
	}

	if runtime.GOOS != "darwin" {
		fmt.Fprintln(e.Stderr, "cwatch: releases exist only for macOS")
		return ExitError
	}
	exe, err := e.Executable()
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: cannot find the running binary:", err)
		return ExitError
	}
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".cwatch-upgrade-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintf(e.Stderr, "cwatch: you cannot write to %s. Run this command:\n  sudo %s upgrade\n", dir, exe)
			return ExitError
		}
		fmt.Fprintln(e.Stderr, "cwatch:", err)
		return ExitError
	}
	next := probe.Name()
	probe.Close()
	defer os.Remove(next) // no effect after the rename

	fmt.Fprintf(e.Stderr, "Downloading cwatch %s…\n", latest)
	if err := e.fetchRelease(ctx, rel, tag, next); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch:", err)
		return ExitError
	}
	// The new binary must run and report the release version.
	out, err := exec.CommandContext(ctx, next, "version").Output()
	reported := strings.TrimSpace(string(out))
	if err != nil || (reported != "cwatch "+latest && !strings.HasPrefix(reported, "cwatch "+latest+" ")) {
		fmt.Fprintf(e.Stderr, "cwatch: the downloaded binary does not report version %s (%q, %v). Nothing changed.\n", latest, reported, err)
		return ExitError
	}
	if err := os.Rename(next, exe); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: cannot replace the binary:", err)
		return ExitError
	}
	fmt.Fprintf(e.Stdout, "Upgraded cwatch from %s to %s at %s.\n", o.Current, latest, exe)
	fmt.Fprintln(e.Stdout, "The hooks use the same path, so you do not need to run setup again.")
	return ExitOK
}

// fetchRelease downloads the archive for this architecture, checks it
// against SHA256SUMS, and writes the binary to dst with mode 0755.
func (e *Env) fetchRelease(ctx context.Context, rel Releases, tag, dst string) error {
	tmp, err := os.MkdirTemp("", "cwatch-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	name := "cwatch-darwin-" + runtime.GOARCH + ".tar.gz"
	archive, sums := filepath.Join(tmp, name), filepath.Join(tmp, "SHA256SUMS")
	if err := rel.Download(ctx, tag, name, archive); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	if err := rel.Download(ctx, tag, "SHA256SUMS", sums); err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return err
	}
	got, err := fileSHA256(archive)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the SHA-256 of %s is %s, but SHA256SUMS gives %s. Nothing changed", name, got, want)
	}
	return extractBinary(archive, dst)
}

func checksumFor(path, name string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, maxChecksumsBytes))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary writes the regular file "cwatch" of the archive to dst.
func extractBinary(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return errors.New("the archive has no cwatch binary")
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if filepath.Clean(h.Name) != "cwatch" || h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size > maxBinaryBytes {
			return fmt.Errorf("the binary in the archive is too large (%d bytes)", h.Size)
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxBinaryBytes)); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dst, 0o755)
	}
}

type version struct {
	nums [3]int
	pre  string
}

// parseVersion parses "1.2.3" or "1.2.3-rc.1". It rejects "dev".
func parseVersion(s string) (version, bool) {
	var v version
	s = strings.TrimPrefix(s, "v")
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	return v, true
}

// compareVersions returns -1, 0, or 1. A pre-release is older than the
// release with the same numbers.
func compareVersions(a, b version) int {
	for i := range a.nums {
		if a.nums[i] != b.nums[i] {
			if a.nums[i] < b.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	case a.pre < b.pre:
		return -1
	}
	return 1
}
