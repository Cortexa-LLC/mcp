package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// indexLayerFromSource writes a scope config and indexes real source into that
// scope's database, so the resulting graph is what `kg index` would actually
// produce rather than hand-built nodes.
func indexLayerFromSource(t *testing.T, aiDir, name string, layers []string, files map[string]string) {
	t.Helper()

	scopeDir := filepath.Join(aiDir, "scope")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := ScopeConfig{Name: name, Database: name + ".db", Layers: layers}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal scope %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(scopeDir, name+".json"), data, 0o644); err != nil {
		t.Fatalf("write scope %s: %v", name, err)
	}

	srcDir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll src: %v", err)
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}

	store, err := OpenStore(filepath.Join(aiDir, cfg.Database))
	if err != nil {
		t.Fatalf("OpenStore %s: %v", name, err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, fedProject, srcDir)
	if err != nil {
		t.Fatalf("NewIndexer %s: %v", name, err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("Index %s: %v", name, err)
	}
}

// The end-to-end claim: cross-layer package linking derives real edges from
// what the indexer actually produces, not only from hand-built fixtures.
//
// This could not pass before JVM package declarations were indexed — the only
// package entities were Go's bare identifiers, which never reach
// minPackageSegments, so packageIndex filtered every one of them out and
// LinkPackages returned Derived: 0 against any indexed corpus.
func TestLinkPackagesDerivesFromIndexedJVMSource(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	aiDir := t.TempDir()

	// The library layer declares the package.
	indexLayerFromSource(t, aiDir, "libraries", nil, map[string]string{
		"AuthClient.java": "package com.depop.auth.client;\n\npublic class AuthClient {}\n",
	})
	// The consumer layer imports a symbol from it.
	indexLayerFromSource(t, aiDir, "martech", []string{"libraries"}, map[string]string{
		"Checkout.java": "package com.depop.martech.checkout;\n\nimport com.depop.auth.client.AuthClient;\n\npublic class Checkout {}\n",
	})

	scope, err := LoadScopeConfig(aiDir, "martech")
	if err != nil {
		t.Fatalf("LoadScopeConfig: %v", err)
	}
	_, report, err := LoadFederatedGraph(FederatedGraphOptions{
		AIDir: aiDir, Scope: scope, ProjectID: fedProject,
	})
	if err != nil {
		t.Fatalf("LoadFederatedGraph: %v", err)
	}

	if report.Link.Derived < 1 {
		t.Errorf("Derived = %d, want at least 1 — no cross-layer edge was derived from indexed source",
			report.Link.Derived)
	}
}
