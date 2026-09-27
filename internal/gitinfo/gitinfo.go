// Package gitinfo reads the current branch from the .git directory. It does
// not run git. Only the list and dashboard commands use it, never the hook.
package gitinfo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cwatch/internal/textutil"
)

const maxDepth = 32

// Branch returns the branch name for the repository that contains dir. It
// returns a short commit ID for a detached HEAD, and "" when dir is not in a
// repository.
func Branch(dir string) string {
	for i := 0; i < maxDepth && dir != ""; i++ {
		gitPath := filepath.Join(dir, ".git")
		fi, err := os.Stat(gitPath)
		if err == nil {
			gitDir := gitPath
			if !fi.IsDir() {
				gitDir = readGitFile(gitPath, dir)
				if gitDir == "" {
					return ""
				}
			}
			return readHead(filepath.Join(gitDir, "HEAD"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// readGitFile reads a ".git" file of a worktree or submodule.
func readGitFile(path, base string) string {
	data, err := readSmall(path)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	d := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(d) {
		d = filepath.Join(base, d)
	}
	return d
}

func readHead(path string) string {
	data, err := readSmall(path)
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		ref = strings.TrimSpace(ref)
		ref = strings.TrimPrefix(ref, "refs/heads/")
		return textutil.OneLine(ref, 80)
	}
	if len(head) >= 7 {
		return textutil.OneLine(head[:7], 7)
	}
	return ""
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf[:n]), nil
}

// Cache caches branch names by directory for one refresh cycle.
type Cache struct {
	mu sync.Mutex
	m  map[string]string
}

// Get returns the branch for dir.
func (c *Cache) Get(dir string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]string{}
	}
	if b, ok := c.m[dir]; ok {
		return b
	}
	b := Branch(dir)
	c.m[dir] = b
	return b
}
