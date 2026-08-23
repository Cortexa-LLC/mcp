package knowledge

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/remote"
	"github.com/cortexa-llc/mcp/kglib"
)

// FederatedStore merges query results across several KG databases. The
// implementation and its merge semantics live in kglib (see
// kglib/README.md#federated-mode); this file only turns kg's scope
// configuration into the layer list kglib expects.
type (
	FederatedStore = kglib.FederatedStore
	LayerConfig    = kglib.LayerConfig
)

// OpenFederatedStore opens a store with its configured layers.
// The primary scope's store is opened read-write (unless readOnly), layers are
// read-only.
func OpenFederatedStore(aiDir string, scopeConfig *ScopeConfig, readOnly bool) (*FederatedStore, error) {
	return OpenFederatedStoreWithExtra(aiDir, scopeConfig, readOnly, nil)
}

// OpenFederatedStoreWithExtra is OpenFederatedStore plus caller-supplied layers,
// used to federate in a database that is not one of the project's scopes — the
// user-level personal store, for instance. Extra layers keep the priority they
// arrive with, so pass a priority below 1 to rank them under every scope layer.
//
// Ownership note: the returned store owns every layer it opened, including the
// extra ones, and Close() closes them all. Do not close an extra layer's store
// yourself.
func OpenFederatedStoreWithExtra(aiDir string, scopeConfig *ScopeConfig, readOnly bool, extra []LayerConfig) (*FederatedStore, error) {
	layers := make([]LayerConfig, 0, len(scopeConfig.Layers)+len(extra)+1)

	// closeAll releases the stores opened so far, for the error paths below.
	closeAll := func() {
		for _, l := range layers {
			if l.Store != nil {
				l.Store.Close()
			}
		}
	}

	// Extra layers first: they are ordered lowest-priority, and kglib expects
	// the primary store last.
	layers = append(layers, extra...)

	// Remote hub layers rank above the extras (e.g. the personal store) but
	// below every local layer. LayerConfig.ProjectID stays empty: the hub
	// resolves each graph's own project ID, and the remote layer ignores the
	// projectID argument entirely.
	remoteCount := 0
	if len(scopeConfig.Remotes) > 0 {
		hubURL, err := GetHubURL(aiDir)
		if err != nil || hubURL == "" {
			fmt.Fprintf(os.Stderr, "Warning: scope %s lists remotes but no hub is configured in .ai/config.json — skipping remote layers\n", scopeConfig.Name)
		} else {
			readToken := os.Getenv("KG_HUB_READ_TOKEN")
			for i, graph := range scopeConfig.Remotes {
				layers = append(layers, LayerConfig{
					Name:     "remote:" + graph,
					Store:    remote.NewLayer(hubURL, graph, readToken),
					Priority: i + 1,
				})
			}
			remoteCount = len(scopeConfig.Remotes)
		}
	}

	// Layer stores are always read-only — a scope can never write to a layer.
	for i, layerName := range scopeConfig.Layers {
		layerCfg, err := LoadScopeConfig(aiDir, layerName)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("load layer %s: %w", layerName, err)
		}

		store, err := OpenStoreReadOnly(filepath.Join(aiDir, layerCfg.Database))
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("open layer %s: %w", layerName, err)
		}

		layers = append(layers, LayerConfig{
			Name:     layerName,
			Store:    store,
			Priority: remoteCount + i + 1, // Local layers rank above the remotes
		})
	}

	// The primary scope's store goes last, at the highest priority.
	primaryPath := filepath.Join(aiDir, scopeConfig.Database)
	var primaryStore *Store
	var err error
	if readOnly {
		primaryStore, err = OpenStoreReadOnly(primaryPath)
	} else {
		primaryStore, err = OpenStore(primaryPath)
	}
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("open primary store: %w", err)
	}

	layers = append(layers, LayerConfig{
		Name:     scopeConfig.Name,
		Store:    primaryStore,
		Priority: remoteCount + len(scopeConfig.Layers) + 10, // Highest priority
	})

	return kglib.NewFederatedStore(layers), nil
}
