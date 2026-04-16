package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// findProjectRoot determines the project root directory using a three-stage strategy:
//
//  1. Walk upward from dir looking for an existing .ai directory — an explicit
//     signal that kg has been initialised for that subtree (handles monorepos and
//     subprojects where the user wants a scoped knowledge graph).
//
//  2. Run "git rev-parse --show-toplevel" — returns the current git repository root,
//     which is the submodule's own root when inside a git submodule (not the parent
//     repo's root), so submodule boundaries are respected automatically.
//
//  3. Walk upward looking for other project-root markers (go.mod, package.json, …)
//     for non-git projects.
//
// Falls back to dir if none of the above succeed.
func findProjectRoot(dir string) string {
	// Stage 1: explicit .ai directory (highest priority — user-chosen scope)
	for cur := dir; ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, ".ai")); err == nil {
			return cur
		}
		if parent := filepath.Dir(cur); parent == cur {
			break
		}
	}

	// Stage 2: git repository root (handles submodules correctly)
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if root != "" {
			return root
		}
	}

	// Stage 3: common project-root markers for non-git projects
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "CLAUDE.md"}
	for cur := dir; ; cur = filepath.Dir(cur) {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(cur, m)); err == nil {
				return cur
			}
		}
		if parent := filepath.Dir(cur); parent == cur {
			break
		}
	}

	// Fallback: use dir as-is
	return dir
}

// projectIDFromCwd returns the project ID for the given working directory.
// The project ID is the base name of the project root.
func projectIDFromCwd(cwd string) string {
	return filepath.Base(findProjectRoot(cwd))
}

// openStore locates the project root, opens the knowledge graph store in
// read-write mode at <root>/.ai/knowledge.db, and returns store+projectID.
// Use this for commands that modify the graph (index, add, link).
// Callers must defer store.Close().
func openStore() (*knowledge.Store, string, error) {
	return openStoreMode(false)
}

// openStoreRO opens the store in read-only mode, allowing concurrent access
// alongside other readers. Use for search, stats, show, and the MCP server.
// Callers must defer store.Close().
func openStoreRO() (*knowledge.Store, string, error) {
	return openStoreMode(true)
}


func openStoreMode(readOnly bool) (*knowledge.Store, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("get cwd: %w", err)
	}
	root := findProjectRoot(cwd)
	dbPath := filepath.Join(root, ".ai", "knowledge.db")
	var store *knowledge.Store
	if readOnly {
		store, err = knowledge.OpenStoreReadOnly(dbPath)
	} else {
		store, err = knowledge.OpenStore(dbPath)
	}
	if err != nil {
		return nil, "", err
	}
	return store, filepath.Base(root), nil
}
