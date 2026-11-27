package renameio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloseAtomicallyReplaceCreatesFile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "index.bin")

	f, err := TempFile("", dest)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	if _, err := f.Write([]byte("payload")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.CloseAtomicallyReplace(); err != nil {
		t.Fatalf("CloseAtomicallyReplace: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content = %q, want %q", got, "payload")
	}
}

func TestCloseAtomicallyReplaceOverwritesExisting(t *testing.T) {
	// The case that fails on Windows if os.Rename is not replace-capable, and
	// the one hnsw relies on every time it re-saves a vector index.
	dest := filepath.Join(t.TempDir(), "index.bin")
	if err := os.WriteFile(dest, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, err := TempFile("", dest)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	if _, err := f.Write([]byte("fresh")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.CloseAtomicallyReplace(); err != nil {
		t.Fatalf("CloseAtomicallyReplace: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "fresh" {
		t.Errorf("content = %q, want %q", got, "fresh")
	}
}

func TestTempFileIsCreatedBesideDestination(t *testing.T) {
	// Atomic replacement requires the temporary file to share a volume with the
	// destination; keeping it in the same directory is what guarantees that.
	dir := t.TempDir()
	dest := filepath.Join(dir, "index.bin")

	f, err := TempFile("", dest)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer f.Cleanup()

	if got := filepath.Dir(f.Name()); got != dir {
		t.Errorf("temp file in %q, want %q", got, dir)
	}
}

func TestCleanupRemovesTempFileAndLeavesDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "index.bin")

	f, err := TempFile("", dest)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	tmpName := f.Name()
	if err := f.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(tmpName); !os.IsNotExist(err) {
		t.Errorf("temp file %q still present (stat err %v)", tmpName, err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("destination %q should not have been created", dest)
	}
}

func TestCleanupAfterReplaceIsNoOp(t *testing.T) {
	// Callers defer Cleanup and then call CloseAtomicallyReplace, so Cleanup
	// must not delete the file it just published.
	dest := filepath.Join(t.TempDir(), "index.bin")

	f, err := TempFile("", dest)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	if _, err := f.Write([]byte("kept")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.CloseAtomicallyReplace(); err != nil {
		t.Fatalf("CloseAtomicallyReplace: %v", err)
	}
	if err := f.Cleanup(); err != nil {
		t.Errorf("Cleanup after replace returned %v, want nil", err)
	}

	if got, err := os.ReadFile(dest); err != nil || string(got) != "kept" {
		t.Errorf("ReadFile = %q, %v; want %q, nil", got, err, "kept")
	}
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "index.bin")
	if err := os.WriteFile(dest, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(dest, []byte("fresh"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "fresh" {
		t.Errorf("content = %q, want %q", got, "fresh")
	}
}

func TestTempDirIsDestinationDir(t *testing.T) {
	dir := t.TempDir()
	if got := TempDir(filepath.Join(dir, "index.bin")); got != dir {
		t.Errorf("TempDir() = %q, want %q", got, dir)
	}
}
