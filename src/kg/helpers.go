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

// pkSeparator mirrors the Unit Separator (0x1F) that kglib.PK joins project_id
// and id with to form a collision-free primary key. project_id must never
// contain it, or the pk for a whole project would be corrupted.
const pkSeparator = '\x1f'

// projectIDFromCwd returns the project ID for the given working directory.
// The project ID is the base name of the project root.
//
// It also guards the one invariant kglib.PK rests on — that project_id never
// contains the 0x1F separator. A repo directory basename holding 0x1F is
// effectively impossible, so this is defending the invariant at its single
// derivation site rather than handling a real case: if one ever appeared we
// replace the byte (non-fatally, with a warning) instead of letting a raw
// separator reach PK and silently collapse every pk for the project.
func projectIDFromCwd(cwd string) string {
	id := filepath.Base(findProjectRoot(cwd))
	if strings.ContainsRune(id, pkSeparator) {
		fmt.Fprintf(os.Stderr,
			"Warning: project_id %q contains the reserved 0x1F separator; replacing with '_' to preserve primary-key uniqueness\n", id)
		id = strings.ReplaceAll(id, string(pkSeparator), "_")
	}
	return id
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
	return openStoreModeWithScope(readOnly, "")
}

// openStoreModeWithScope opens a store for a specific scope.
// If scopeName is empty, uses legacy single-DB mode (knowledge.db).
func openStoreModeWithScope(readOnly bool, scopeName string) (*knowledge.Store, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("get cwd: %w", err)
	}
	root := findProjectRoot(cwd)
	aiDir := filepath.Join(root, ".ai")

	// Determine database path
	var dbPath string
	if scopeName != "" {
		// Load scope config to get database name
		cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
		if err != nil {
			return nil, "", fmt.Errorf("load scope %s: %w", scopeName, err)
		}
		dbPath = filepath.Join(aiDir, cfg.Database)
	} else {
		// Legacy mode: use knowledge.db
		dbPath = filepath.Join(aiDir, "knowledge.db")
	}

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
