package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The whole point of IndexStats.BinarySkipped is observability: a legitimate but
// binary-looking source file that gets skipped must not be invisible. The CLI
// path surfaces it (index.go); index_project is the only other caller that
// builds a user-facing summary, so it must surface it too. These tests pin the
// MCP summary line to the counter in both directions.
func TestMCPIndexProjectSurfacesBinarySkipped(t *testing.T) {
	root := t.TempDir()
	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A normal source file so indexing has real work to do...
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	// ...plus a file sharing a source extension whose head holds a NUL byte,
	// which the tree-sitter path skips as binary and counts in BinarySkipped.
	if err := os.WriteFile(filepath.Join(root, "blob.go"),
		[]byte("package x\x00\x00binary"), 0644); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	dbPath := filepath.Join(aiDir, "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	_, handlers := buildTools(aiDir, nil, "testproj", root, PersonalConfig{})
	res, err := mcpCall(t, handlers, "index_project", map[string]interface{}{})
	if err != nil {
		t.Fatalf("index_project: %v", err)
	}
	msg, ok := res.(string)
	if !ok {
		t.Fatalf("index_project returned %T, want string", res)
	}
	if !strings.Contains(msg, "Binary files skipped: 1") {
		t.Errorf("summary missing binary-skip line; got:\n%s", msg)
	}
}

func TestMCPIndexProjectOmitsBinarySkippedWhenZero(t *testing.T) {
	root := t.TempDir()
	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	dbPath := filepath.Join(aiDir, "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	_, handlers := buildTools(aiDir, nil, "testproj", root, PersonalConfig{})
	res, err := mcpCall(t, handlers, "index_project", map[string]interface{}{})
	if err != nil {
		t.Fatalf("index_project: %v", err)
	}
	msg, ok := res.(string)
	if !ok {
		t.Fatalf("index_project returned %T, want string", res)
	}
	if strings.Contains(msg, "Binary files skipped") {
		t.Errorf("summary surfaced a binary-skip line with no binaries; got:\n%s", msg)
	}
}
