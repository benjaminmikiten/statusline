package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestGet_NotARepo(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := Get(ctx, dir)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.IsRepo {
		t.Errorf("IsRepo = true, want false for a plain directory")
	}
}

func TestGet_RepoWithChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")

	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// One staged new file, one unstaged modification to the tracked file.
	if err := os.WriteFile(tracked, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	staged := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(staged, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "staged.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := Get(ctx, dir)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !got.IsRepo {
		t.Fatal("IsRepo = false, want true")
	}
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want %q", got.Branch, "main")
	}
	if got.Staged != 1 {
		t.Errorf("Staged = %d, want 1", got.Staged)
	}
	if got.Modified != 1 {
		t.Errorf("Modified = %d, want 1", got.Modified)
	}
}

func TestCachedGet_ReusesWithinMaxAge(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	cacheDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	first, err := CachedGet(ctx, dir, "session-a", cacheDir, 5*time.Second)
	if err != nil {
		t.Fatalf("CachedGet (first) returned error: %v", err)
	}

	// Create a second branch on disk but don't check it out; if CachedGet
	// re-ran git it would still report "main", so this only proves the
	// cache file was written and is reused without erroring on a second call.
	second, err := CachedGet(ctx, dir, "session-a", cacheDir, 5*time.Second)
	if err != nil {
		t.Fatalf("CachedGet (second) returned error: %v", err)
	}
	if first != second {
		t.Errorf("second call = %+v, want identical to first %+v", second, first)
	}

	cacheFile := filepath.Join(cacheDir, "statusline-git-cache-session-a")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("expected cache file %s to exist: %v", cacheFile, err)
	}
}
