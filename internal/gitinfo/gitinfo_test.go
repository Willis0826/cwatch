package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBranch(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o700)
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o600)
	sub := filepath.Join(root, "a", "b")
	os.MkdirAll(sub, 0o700)
	if got := Branch(sub); got != "feature/x" {
		t.Fatalf("branch %q", got)
	}

	// A worktree has a .git file.
	wt := t.TempDir()
	gd := filepath.Join(root, ".git", "worktrees", "wt")
	os.MkdirAll(gd, 0o700)
	os.WriteFile(filepath.Join(gd, "HEAD"), []byte("0123456789abcdef\n"), 0o600)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o600)
	if got := Branch(wt); got != "0123456" {
		t.Fatalf("detached worktree %q", got)
	}

	if got := Branch(t.TempDir()); got != "" {
		t.Fatalf("no repository: %q", got)
	}
}
