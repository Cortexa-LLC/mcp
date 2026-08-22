// Package hub implements a shared knowledge-graph hub: a small HTTP service
// that hosts read-only knowledge graphs seeded via `kg push` and answers
// search queries over them (docs/kg-shared-service-design.md, Phase 2).
package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GraphInfo describes one hosted graph: its provenance (from the pushed
// database's KGMeta stamp) plus hub-side bookkeeping.
type GraphInfo struct {
	Repo      string    `json:"repo,omitempty"`
	Commit    string    `json:"commit"`
	Dirty     bool      `json:"dirty,omitempty"`
	IndexedAt time.Time `json:"indexedAt"`
	PushedAt  time.Time `json:"pushedAt"`
	KGVersion string    `json:"kgVersion,omitempty"`
	ProjectID string    `json:"projectID"`
	Layers    []string  `json:"layers,omitempty"`
}

// Registry is the hub's persistent index of hosted graphs, stored as
// registry.json in the data directory.
type Registry struct {
	Graphs map[string]*GraphInfo `json:"graphs"`
}

const registryFile = "registry.json"

// loadRegistry reads registry.json from dataDir. A missing file yields an
// empty registry, not an error.
func loadRegistry(dataDir string) (*Registry, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, registryFile))
	if os.IsNotExist(err) {
		return &Registry{Graphs: map[string]*GraphInfo{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}

	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	if r.Graphs == nil {
		r.Graphs = map[string]*GraphInfo{}
	}
	return &r, nil
}

// saveRegistry writes the registry atomically: marshal to registry.json.tmp,
// then rename over registry.json.
func saveRegistry(dataDir string, r *Registry) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}

	path := filepath.Join(dataDir, registryFile)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	// fsync before the rename: without it a crash can commit an empty or
	// truncated registry.json.
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync registry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close registry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit registry: %w", err)
	}
	return nil
}
