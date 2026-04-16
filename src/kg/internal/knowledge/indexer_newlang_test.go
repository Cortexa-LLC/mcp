package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIndexer_MarkdownFile verifies Markdown heading and mermaid entity extraction.
func TestIndexer_MarkdownFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `# Architecture Overview

## Components

Some description.

### Database Layer

Uses KuzuDB.

#### Ignored Deep Section

Not indexed (H4).

` + "```mermaid" + `
graph TD
    A[API Server] --> B[Database]
    B --> C[(Cache)]
    D{Load Balancer} --> A
` + "```" + `

` + "```go" + `
func main() {}
` + "```" + `
`
	if err := os.WriteFile(filepath.Join(srcDir, "arch.md"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// H1–H3 headings indexed as topics
	if !entityExistsByName(t, store, "Architecture Overview", EntityTypeTopic) {
		t.Error("expected H1 'Architecture Overview' as topic")
	}
	if !entityExistsByName(t, store, "Components", EntityTypeTopic) {
		t.Error("expected H2 'Components' as topic")
	}
	if !entityExistsByName(t, store, "Database Layer", EntityTypeTopic) {
		t.Error("expected H3 'Database Layer' as topic")
	}

	// H4 must NOT be indexed
	if entityExistsByName(t, store, "Ignored Deep Section", "") {
		t.Error("H4 'Ignored Deep Section' must NOT be indexed")
	}

	// Mermaid node labels
	if !entityExistsByName(t, store, "API Server", EntityTypeType) {
		t.Error("expected mermaid node 'API Server' as type")
	}
	if !entityExistsByName(t, store, "Database", EntityTypeType) {
		t.Error("expected mermaid node 'Database' as type")
	}
	if !entityExistsByName(t, store, "Load Balancer", EntityTypeType) {
		t.Error("expected mermaid node 'Load Balancer' as type")
	}

	// Go fenced block must NOT generate entities (not mermaid)
	if entityExistsByName(t, store, "main", EntityTypeFunction) {
		t.Error("go code block inside markdown must NOT be indexed")
	}
}

// TestIndexer_BashFile verifies bash function and source-include extraction.
func TestIndexer_BashFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `#!/bin/bash
# deploy script

source ./lib/utils.sh
. ./lib/common.sh

setup() {
    echo "setup"
}

function deploy {
    echo "deploying"
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "deploy.sh"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Functions
	if !entityExistsByName(t, store, "setup", EntityTypeFunction) {
		t.Error("expected 'setup' (function) to be created")
	}
	if !entityExistsByName(t, store, "deploy", EntityTypeFunction) {
		t.Error("expected 'deploy' (function) to be created")
	}

	// Source includes
	if !entityExistsByName(t, store, "./lib/utils.sh", EntityTypeImport) {
		t.Error("expected './lib/utils.sh' (import) to be created")
	}
	if !entityExistsByName(t, store, "./lib/common.sh", EntityTypeImport) {
		t.Error("expected './lib/common.sh' (import) to be created")
	}
}

// TestIndexer_GroovyFile verifies Groovy class, function, and import extraction.
func TestIndexer_GroovyFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `package com.example

import groovy.json.JsonSlurper
import groovy.transform.CompileStatic

class MyService {
    def greet() {
        return "Hello"
    }
}

def helper() {
    println "helper"
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "service.groovy"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	if !entityExistsByName(t, store, "MyService", EntityTypeType) {
		t.Error("expected 'MyService' (type) to be created")
	}
	if !entityExistsByName(t, store, "groovy.json.JsonSlurper", EntityTypeImport) {
		t.Error("expected 'groovy.json.JsonSlurper' (import) to be created")
	}
}

// TestIndexer_CSSFile verifies CSS selector extraction.
func TestIndexer_CSSFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `/* Styles */
.container {
    max-width: 1200px;
}

#header {
    background: blue;
}

.nav-bar {
    display: flex;
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "styles.css"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	if !entityExistsByName(t, store, ".container", EntityTypeType) {
		t.Error("expected '.container' (type) CSS selector to be created")
	}
	if !entityExistsByName(t, store, "#header", EntityTypeType) {
		t.Error("expected '#header' (type) CSS selector to be created")
	}
	if !entityExistsByName(t, store, ".nav-bar", EntityTypeType) {
		t.Error("expected '.nav-bar' (type) CSS selector to be created")
	}
}

// TestIndexer_YAMLFile verifies YAML key and named-value extraction.
func TestIndexer_YAMLFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: production
spec:
  replicas: 3
`
	if err := os.WriteFile(filepath.Join(srcDir, "deploy.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Top-level structural keys
	if !entityExistsByName(t, store, "metadata", EntityTypeType) {
		t.Error("expected 'metadata' (type) top-level key to be created")
	}
	if !entityExistsByName(t, store, "spec", EntityTypeType) {
		t.Error("expected 'spec' (type) top-level key to be created")
	}

	// kind: value is a recognised name key
	if !entityExistsByName(t, store, "Deployment", EntityTypeType) {
		t.Error("expected 'Deployment' (type) from kind: value to be created")
	}

	// metadata.name is a recognised name key
	if !entityExistsByName(t, store, "my-app", EntityTypeType) {
		t.Error("expected 'my-app' (type) from name: value to be created")
	}
}

// TestIndexer_HTMLFile verifies HTML heading and ID extraction.
func TestIndexer_HTMLFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `<!DOCTYPE html>
<html>
<head><title>My Application</title></head>
<body>
  <h1 id="main-header">Welcome</h1>
  <h2>About Us</h2>
  <h3 id="contact">Contact</h3>
  <h4>Ignored Deep</h4>
  <div id="sidebar">content</div>
</body>
</html>
`
	if err := os.WriteFile(filepath.Join(srcDir, "index.html"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	if stats.FilesScanned < 1 {
		t.Errorf("expected at least 1 file scanned, got %d", stats.FilesScanned)
	}

	// Title and headings as topics
	if !entityExistsByName(t, store, "My Application", EntityTypeTopic) {
		t.Error("expected <title> 'My Application' as topic")
	}
	if !entityExistsByName(t, store, "Welcome", EntityTypeTopic) {
		t.Error("expected <h1> 'Welcome' as topic")
	}
	if !entityExistsByName(t, store, "About Us", EntityTypeTopic) {
		t.Error("expected <h2> 'About Us' as topic")
	}
	if !entityExistsByName(t, store, "Contact", EntityTypeTopic) {
		t.Error("expected <h3> 'Contact' as topic")
	}

	// H4 must NOT be indexed
	if entityExistsByName(t, store, "Ignored Deep", "") {
		t.Error("H4 'Ignored Deep' must NOT be indexed")
	}

	// id attributes as types
	if !entityExistsByName(t, store, "main-header", EntityTypeType) {
		t.Error("expected id='main-header' (type) to be created")
	}
	if !entityExistsByName(t, store, "sidebar", EntityTypeType) {
		t.Error("expected id='sidebar' (type) to be created")
	}
}

// TestIndexer_SkipsAlwaysSkipDirs verifies that .git and node_modules are never walked.
func TestIndexer_SkipsAlwaysSkipDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Real source file
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nfunc RealFunc() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Files inside always-skip directories should be ignored
	gitDir := filepath.Join(srcDir, ".git", "objects")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "fake.go"), []byte("package git\nfunc GitInternal() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile .git: %v", err)
	}

	nmDir := filepath.Join(srcDir, "node_modules", "lodash")
	if err := os.MkdirAll(nmDir, 0755); err != nil {
		t.Fatalf("MkdirAll node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("function lodashInternal() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile node_modules: %v", err)
	}

	store, stats := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	// Only main.go should be scanned
	if stats.FilesScanned != 1 {
		t.Errorf("expected exactly 1 file scanned (main.go), got %d", stats.FilesScanned)
	}

	if !entityExistsByName(t, store, "RealFunc", EntityTypeFunction) {
		t.Error("expected 'RealFunc' to be indexed")
	}
	if entityExistsByName(t, store, "GitInternal", "") {
		t.Error("'GitInternal' inside .git must NOT be indexed")
	}
	if entityExistsByName(t, store, "lodashInternal", "") {
		t.Error("'lodashInternal' inside node_modules must NOT be indexed")
	}
}
