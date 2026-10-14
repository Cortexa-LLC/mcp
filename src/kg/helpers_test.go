package main

import (
	"os"
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
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := projectIDFromCwd(dir)

	if strings.ContainsRune(got, pkSeparator) {
		t.Fatalf("project_id still contains reserved 0x1F separator: %q", got)
	}
	if got != "weird_name" {
		t.Fatalf("project_id = %q, want %q", got, "weird_name")
	}
}
