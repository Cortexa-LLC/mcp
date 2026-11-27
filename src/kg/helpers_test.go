package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A project_id derived from a repo directory basename must never contain the
// 0x1F Unit Separator that kglib.PK uses to join project_id and id, or the pk
// for the whole project would collapse. projectIDFromCwd guards that invariant
// at its single derivation site.
func TestProjectIDFromCwdReplacesSeparator(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "weird\x1fname")

	// The directory is deliberately not created. Windows rejects control
	// characters 0x01-0x1F in a filename, so Mkdir here failed with "The
	// filename, directory name, or volume label syntax is incorrect" and the
	// assertion below never ran. It does not need to exist: projectIDFromCwd
	// derives the id from the path string, and findProjectRoot falls back to
	// the path as-is once it finds no marker above it — which is just as true
	// of a directory that is absent as of an empty one.
	got := projectIDFromCwd(dir)

	if strings.ContainsRune(got, pkSeparator) {
		t.Fatalf("project_id still contains reserved 0x1F separator: %q", got)
	}
	if got != "weird_name" {
		t.Fatalf("project_id = %q, want %q", got, "weird_name")
	}
}
