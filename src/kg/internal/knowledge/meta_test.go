package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRun runs git in dir with identity config flags so commits work in
// environments with no global git config, and with signing/hooks disabled so
// a developer's global commit.gpgsign or core.hooksPath cannot break tests.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.email=t@t", "-c", "user.name=t",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "commit", "-m", "initial", "--allow-empty")
	return dir
}

func newMetaTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStampMetaGitRepo(t *testing.T) {
	repo := newTestGitRepo(t)
	head := gitRun(t, repo, "rev-parse", "HEAD")
	store := newMetaTestStore(t)

	meta, err := StampMeta(store, "proj", repo, "1.2.3")
	if err != nil {
		t.Fatalf("StampMeta: %v", err)
	}
	if meta.Commit != head {
		t.Errorf("Commit = %q, want HEAD %q", meta.Commit, head)
	}
	if meta.Dirty {
		t.Error("clean repo stamped as dirty")
	}
	if time.Since(meta.IndexedAt) > time.Minute || meta.IndexedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("IndexedAt not recent: %v", meta.IndexedAt)
	}
	if meta.KGVersion != "1.2.3" {
		t.Errorf("KGVersion = %q, want 1.2.3", meta.KGVersion)
	}

	got, err := store.GetMeta("proj")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got == nil {
		t.Fatal("GetMeta returned nil after StampMeta")
	}
	if got.Commit != meta.Commit || got.Dirty != meta.Dirty || got.KGVersion != meta.KGVersion {
		t.Errorf("stored stamp mismatch: got %+v, stamped %+v", got, meta)
	}
}

func TestStampMetaDirty(t *testing.T) {
	repo := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	store := newMetaTestStore(t)

	meta, err := StampMeta(store, "proj", repo, "dev")
	if err != nil {
		t.Fatalf("StampMeta: %v", err)
	}
	if !meta.Dirty {
		t.Error("repo with untracked file should stamp as dirty")
	}
}

func TestStampMetaNonGit(t *testing.T) {
	dir := t.TempDir()
	store := newMetaTestStore(t)

	meta, err := StampMeta(store, "proj", dir, "dev")
	if err != nil {
		t.Fatalf("StampMeta on non-git dir: %v", err)
	}
	if meta.Commit != "" || meta.RepoURL != "" {
		t.Errorf("non-git stamp should have empty commit/repo, got %+v", meta)
	}
}

func TestGitAheadCount(t *testing.T) {
	repo := newTestGitRepo(t)
	first := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "-m", "second", "--allow-empty")

	n, err := GitAheadCount(repo, first)
	if err != nil {
		t.Fatalf("GitAheadCount: %v", err)
	}
	if n != 1 {
		t.Errorf("GitAheadCount = %d, want 1", n)
	}

	if _, err := GitAheadCount(repo, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("expected an error for a commit unknown to local history")
	}
}
