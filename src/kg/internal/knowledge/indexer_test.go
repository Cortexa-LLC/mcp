package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexer_Index(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a simple test structure
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Create a Go file with imports and functions
	goContent := `package main

import (
	"fmt"
	"strings"
)

// Main function
func main() {
	fmt.Println("test")
	strings.Contains("hello", "lo")
}

// Helper function
func helper() {
	fmt.Println("helper")
}
`
	goPath := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(goPath, []byte(goContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Create a JS file with imports
	jsContent := `import React from 'react';
import { useState } from 'react';

export function Component() {
  return <div>Test</div>;
}
`
	jsPath := filepath.Join(srcDir, "component.jsx")
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		t.Fatalf("WriteFile JS failed: %v", err)
	}

	// Create store and indexer
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// Verify stats
	if stats.FilesScanned == 0 {
		t.Error("Expected some files to be processed")
	}
	if stats.EntitiesCreated == 0 {
		t.Error("Expected some entities to be created")
	}
	if stats.RelationsCreated == 0 {
		t.Error("Expected some relations to be created")
	}

	t.Logf("Index stats: %d files, %d entities, %d relations",
		stats.FilesScanned, stats.EntitiesCreated, stats.RelationsCreated)

	// Query for entities to verify they were created
	result, err := store.query(`MATCH (e:Entity) RETURN count(e)`)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer result.Close()

	if !result.HasNext() {
		t.Error("Query returned no results")
	}
}

func TestIndexer_WithIgnorePatterns(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create directory structure
	srcDir := filepath.Join(tmpDir, "src")
	nodeModules := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll src failed: %v", err)
	}
	if err := os.MkdirAll(nodeModules, 0755); err != nil {
		t.Fatalf("MkdirAll node_modules failed: %v", err)
	}

	// Create .gitignore
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	gitignoreContent := `node_modules/
*.log
.git/
`
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		t.Fatalf("WriteFile .gitignore failed: %v", err)
	}

	// Create a file in src (should be indexed)
	goContent := `package main
func main() {}
`
	srcFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(srcFile, []byte(goContent), 0644); err != nil {
		t.Fatalf("WriteFile src/main.go failed: %v", err)
	}

	// Create a file in node_modules (should be ignored)
	ignoredFile := filepath.Join(nodeModules, "pkg.js")
	if err := os.WriteFile(ignoredFile, []byte("module.exports = {};"), 0644); err != nil {
		t.Fatalf("WriteFile node_modules/pkg.js failed: %v", err)
	}

	// Create store and indexer
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if stats.FilesScanned == 0 {
		t.Error("Expected src/main.go to be processed")
	}

	// Query for all entities and verify node_modules was not indexed
	result, err := store.query(`MATCH (e:Entity) RETURN e.name`)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer result.Close()

	hasNodeModules := false
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("Result Next failed: %v", err)
		}
		name, _ := row.GetValue(0)
		if nameStr, ok := name.(string); ok {
			if nameStr == "node_modules" || filepath.IsAbs(nameStr) && filepath.Base(nameStr) == "pkg.js" {
				hasNodeModules = true
				break
			}
		}
	}

	if hasNodeModules {
		t.Error("node_modules should have been ignored")
	}
}

func TestIndexer_GoFileProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a Go file with specific characteristics we want to test
	goContent := `package testpkg

import (
	"fmt"
	"github.com/example/pkg"
)

// ExportedFunc is documented
func ExportedFunc(arg string) error {
	return fmt.Errorf("test: %s", arg)
}

func privateFunc() {
	pkg.DoSomething()
}

type ExportedType struct {
	Field string
}
`
	goPath := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(goPath, []byte(goContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Create store and indexer
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if stats.FilesScanned != 1 {
		t.Errorf("Expected 1 file processed, got %d", stats.FilesScanned)
	}

	// Verify we have entities for:
	// - file
	// - package
	// - functions (ExportedFunc, privateFunc)
	// - type (ExportedType)
	minExpectedEntities := 5
	if stats.EntitiesCreated < minExpectedEntities {
		t.Errorf("Expected at least %d entities, got %d", minExpectedEntities, stats.EntitiesCreated)
	}

	// Verify we have relations for:
	// - imports (fmt, pkg)
	// - contains (package->file, file->functions, etc)
	minExpectedRelations := 4
	if stats.RelationsCreated < minExpectedRelations {
		t.Errorf("Expected at least %d relations, got %d", minExpectedRelations, stats.RelationsCreated)
	}

	// Query for the package entity
	result, err := store.query(`MATCH (e:Entity {type: 'package', name: 'testpkg'}) RETURN e.name`)
	if err != nil {
		t.Fatalf("Query for package failed: %v", err)
	}
	defer result.Close()

	if !result.HasNext() {
		t.Error("Package entity 'testpkg' not found")
	}
}

func TestIndexer_JSFileProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a JS file with various import patterns
	jsContent := `import React from 'react';
import { useState, useEffect } from 'react';
import * as Utils from './utils';
import Button from './components/Button';

export function Component() {
  const [state, setState] = useState(0);
  return <Button onClick={() => setState(state + 1)}>Click</Button>;
}

export default Component;
`
	jsPath := filepath.Join(tmpDir, "component.jsx")
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Create store and indexer
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if stats.FilesScanned != 1 {
		t.Errorf("Expected 1 file processed, got %d", stats.FilesScanned)
	}

	// Verify we have file entity and import relations
	minExpectedEntities := 1 // at least the file
	if stats.EntitiesCreated < minExpectedEntities {
		t.Errorf("Expected at least %d entities, got %d", minExpectedEntities, stats.EntitiesCreated)
	}

	// Should have multiple import relations (react, utils, Button)
	minExpectedRelations := 3
	if stats.RelationsCreated < minExpectedRelations {
		t.Errorf("Expected at least %d import relations, got %d", minExpectedRelations, stats.RelationsCreated)
	}
}

func TestIndexer_EmptyDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create store and indexer on empty directory
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing on empty directory
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	if stats.FilesScanned != 0 {
		t.Errorf("Expected 0 files processed in empty dir, got %d", stats.FilesScanned)
	}
	if stats.EntitiesCreated != 0 {
		t.Errorf("Expected 0 entities in empty dir, got %d", stats.EntitiesCreated)
	}
	if stats.RelationsCreated != 0 {
		t.Errorf("Expected 0 relations in empty dir, got %d", stats.RelationsCreated)
	}
}

func TestIndexer_MixedLanguages(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create files in multiple supported languages (Go, JS, TS)
	files := map[string]string{
		"main.go": `package main
import "fmt"
func main() { fmt.Println("go") }
`,
		"app.js": `import React from 'react';
export default function App() {}
`,
		"utils.ts": `import { Helper } from './helper';
export class Utils {}
`,
		"component.jsx": `import { Button } from './button';
export function Component() { return <div>Test</div>; }
`,
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile %s failed: %v", name, err)
		}
	}

	// Create store and indexer
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer failed: %v", err)
	}

	// Run indexing
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// Should process all 4 files
	if stats.FilesScanned != 4 {
		t.Errorf("Expected 4 files processed, got %d", stats.FilesScanned)
	}

	// Should have entities and relations from all files
	if stats.EntitiesCreated == 0 {
		t.Error("Expected entities from mixed language files")
	}
	if stats.RelationsCreated == 0 {
		t.Error("Expected relations from mixed language files")
	}

	t.Logf("Mixed language indexing: %d files, %d entities, %d relations",
		stats.FilesScanned, stats.EntitiesCreated, stats.RelationsCreated)
}
