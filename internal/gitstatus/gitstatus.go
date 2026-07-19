// Package gitstatus shells out to git for branch and change-count info,
// with a session-scoped file cache so a slow repo doesn't stall every
// statusline refresh. Caching is keyed by session_id, not PID: the
// statusline binary re-execs on every refresh, so a PID-keyed cache would
// never hit.
package gitstatus

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Status struct {
	IsRepo   bool
	Branch   string
	Staged   int
	Modified int
}

func execGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func countLines(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// Get inspects dir directly (no caching). A non-repo directory returns
// Status{IsRepo: false} with a nil error, not an error — that's an expected
// outcome, not a failure.
func Get(ctx context.Context, dir string) (Status, error) {
	if _, err := execGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return Status{IsRepo: false}, nil
	}

	branch, err := execGit(ctx, dir, "branch", "--show-current")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: branch: %w", err)
	}

	staged, err := execGit(ctx, dir, "diff", "--cached", "--numstat")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: staged diff: %w", err)
	}

	modified, err := execGit(ctx, dir, "diff", "--numstat")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: modified diff: %w", err)
	}

	return Status{
		IsRepo:   true,
		Branch:   strings.TrimSpace(branch),
		Staged:   countLines(staged),
		Modified: countLines(modified),
	}, nil
}

// CachedGet returns a cached Status for (dir, sessionID) if the cache file
// in cacheDir is younger than maxAge, otherwise calls Get and refreshes it.
func CachedGet(ctx context.Context, dir, sessionID, cacheDir string, maxAge time.Duration) (Status, error) {
	cacheFile := filepath.Join(cacheDir, "statusline-git-cache-"+sessionID)

	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) <= maxAge {
			if data, err := os.ReadFile(cacheFile); err == nil {
				if s, ok := decodeStatus(string(data)); ok {
					return s, nil
				}
			}
		}
	}

	status, err := Get(ctx, dir)
	if err != nil {
		return Status{}, err
	}
	_ = os.WriteFile(cacheFile, []byte(encodeStatus(status)), 0o644) // best-effort
	return status, nil
}

func encodeStatus(s Status) string {
	repo := "0"
	if s.IsRepo {
		repo = "1"
	}
	return fmt.Sprintf("%s|%s|%d|%d", repo, s.Branch, s.Staged, s.Modified)
}

func decodeStatus(data string) (Status, bool) {
	parts := strings.SplitN(strings.TrimSpace(data), "|", 4)
	if len(parts) != 4 {
		return Status{}, false
	}
	var s Status
	s.IsRepo = parts[0] == "1"
	s.Branch = parts[1]
	if _, err := fmt.Sscanf(parts[2], "%d", &s.Staged); err != nil {
		return Status{}, false
	}
	if _, err := fmt.Sscanf(parts[3], "%d", &s.Modified); err != nil {
		return Status{}, false
	}
	return s, true
}
