// Package renameio writes files atomically: content lands in a temporary file
// that then replaces the destination in a single rename, so a reader sees
// either the old file or the complete new one, never a half-written one.
//
// This is a drop-in stand-in for github.com/google/renameio, wired in by a
// replace directive in kg/go.mod and kglib/go.mod. Upstream guards every source
// file with a !windows build constraint — in v1 and in v2 — so on Windows the
// package exists but is empty, and github.com/coder/hnsw, which calls
// renameio.TempFile to save a vector index, fails to compile with
// "undefined: renameio.TempFile". hnsw v0.6.1 is its latest release and still
// has that call, so there is no upstream version to move to.
//
// The replace applies on macOS and Linux as well, so this file deliberately
// mirrors upstream's semantics rather than improving on them: the temporary
// file is created with 0600, the destination is replaced only after the
// contents are fsynced, and the parent directory is left unsynced exactly as
// upstream leaves it. Only TempFile, TempDir, PendingFile, and WriteFile are
// provided — the subset this repository's dependencies actually call.
//
// One deliberate deviation: upstream's TempDir probes whether os.TempDir() sits
// on the destination's mount point and uses it when a test rename succeeds.
// This version always uses the destination's own directory, which is what that
// probe falls back to anyway. That guarantees the rename stays within one
// volume — the condition atomic replacement requires, and one that is easy to
// violate on Windows, where os.TempDir() may be on another drive — and it drops
// two temporary file creations per call.
package renameio

import (
	"os"
	"path/filepath"
)

// PendingFile is a temporary file waiting to replace a destination path.
type PendingFile struct {
	*os.File

	path   string
	done   bool
	closed bool
}

// TempDir returns the directory TempFile will create its temporary file in for
// a file destined for dest. It is always dest's own directory, so that the
// replacing rename never has to cross a volume boundary.
func TempDir(dest string) string {
	return filepath.Dir(dest)
}

func tempDir(dir, dest string) string {
	if dir != "" {
		return dir // a caller-specified directory always wins
	}
	return filepath.Dir(dest)
}

// TempFile creates a temporary file to be renamed over path by
// CloseAtomicallyReplace. If dir is empty, the file is created alongside path.
//
// The file's permissions are 0600; call Chmod on the result to change them.
func TempFile(dir, path string) (*PendingFile, error) {
	f, err := os.CreateTemp(tempDir(dir, path), "."+filepath.Base(path))
	if err != nil {
		return nil, err
	}
	return &PendingFile{File: f, path: path}, nil
}

// Cleanup closes and removes the temporary file. It is a no-op once
// CloseAtomicallyReplace has succeeded, which makes it safe to defer.
//
// This method is not safe for concurrent use by multiple goroutines.
func (t *PendingFile) Cleanup() error {
	if t.done {
		return nil
	}
	var closeErr error
	if !t.closed {
		closeErr = t.Close()
	}
	if err := os.Remove(t.Name()); err != nil {
		return err
	}
	return closeErr
}

// CloseAtomicallyReplace flushes the temporary file to disk and renames it over
// the destination, so a concurrent open sees either the previous file or this
// one, with no window in which the path is missing or truncated.
//
// The fsync is not optional: without it a rename can be durable while the data
// behind it is not, leaving a zero-length file after a crash.
//
// This method is not safe for concurrent use by multiple goroutines.
func (t *PendingFile) CloseAtomicallyReplace() error {
	if err := t.Sync(); err != nil {
		return err
	}
	t.closed = true
	if err := t.Close(); err != nil {
		return err
	}
	if err := os.Rename(t.Name(), t.path); err != nil {
		return err
	}
	t.done = true
	return nil
}

// WriteFile atomically creates or replaces filename with data, then sets its
// permissions to perm.
func WriteFile(filename string, data []byte, perm os.FileMode) error {
	t, err := TempFile("", filename)
	if err != nil {
		return err
	}
	defer t.Cleanup()

	if err := t.Chmod(perm); err != nil {
		return err
	}
	if _, err := t.Write(data); err != nil {
		return err
	}
	return t.CloseAtomicallyReplace()
}
