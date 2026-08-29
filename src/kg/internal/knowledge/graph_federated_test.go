package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fedProject = "fedtest"

// fedEntity describes one entity to seed into a layer's database.
type fedEntity struct {
	name       string
	entityType string
}

// seedLayer writes <aiDir>/scope/<name>.json and the database it points at,
// holding the given entities plus a CONTAINS edge from the first entity to
// every other one.
func seedLayer(t *testing.T, aiDir, name string, layers []string, entities ...fedEntity) {
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

	store, err := OpenStore(filepath.Join(aiDir, cfg.Database))
	if err != nil {
		t.Fatalf("OpenStore %s: %v", name, err)
	}
	defer store.Close()

	var first string
	for _, e := range entities {
		created, err := store.CreateEntity(e.name, e.entityType, fedProject)
		if err != nil {
			t.Fatalf("CreateEntity %s/%s: %v", name, e.name, err)
		}
		if first == "" {
			first = created.ID
			continue
		}
		if err := store.CreateRelation(first, created.ID, RelContains, fedProject); err != nil {
			t.Fatalf("CreateRelation %s: %v", name, err)
		}
	}
}

// loadFed is the common call under test.
func loadFed(t *testing.T, aiDir, scopeName string, maxJoin int, only ...string) (*Graph, *FederationReport) {
	t.Helper()

	scope, err := LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		t.Fatalf("LoadScopeConfig: %v", err)
	}
	g, report, err := LoadFederatedGraph(FederatedGraphOptions{
		AIDir:         aiDir,
		Scope:         scope,
		ProjectID:     fedProject,
		OnlyLayers:    only,
		MaxJoinLayers: maxJoin,
	})
	if err != nil {
		t.Fatalf("LoadFederatedGraph: %v", err)
	}
	return g, report
}

// nodeByName returns the single node with the given name, failing if there is
// not exactly one — which is itself the assertion in most of these tests.
func nodeByName(t *testing.T, g *Graph, name string) GraphNode {
	t.Helper()

	var found []GraphNode
	for _, n := range g.nodes {
		if n.Name == name {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one node named %q, got %d", name, len(found))
	}
	return found[0]
}

func TestLoadFederatedGraphJoinsSharedIdentities(t *testing.T) {
	aiDir := t.TempDir()
	seedLayer(t, aiDir, "libs", nil,
		fedEntity{"libs.go", EntityTypeFile},
		fedEntity{"Address", EntityTypeType},
	)
	seedLayer(t, aiDir, "payments", nil,
		fedEntity{"payments.go", EntityTypeFile},
		fedEntity{"Address", EntityTypeType},
		fedEntity{"Charge", EntityTypeType},
	)
	seedLayer(t, aiDir, "estate", []string{"libs", "payments"},
		fedEntity{"estate.md", EntityTypeFile},
	)

	g, report := loadFed(t, aiDir, "estate", 0)

	// Address exists in two layers and must arrive as one node carrying both.
	address := nodeByName(t, g, "Address")
	if got := strings.Join(address.Layers, ","); got != "libs,payments" {
		t.Errorf("Address layers = %q, want \"libs,payments\"", got)
	}
	if report.Joined != 1 {
		t.Errorf("Joined = %d, want 1", report.Joined)
	}
	if report.MergedNodes != 1 {
		t.Errorf("MergedNodes = %d, want 1", report.MergedNodes)
	}

	// Charge is in one layer only: present, unjoined, attributed to its layer.
	charge := nodeByName(t, g, "Charge")
	if got := strings.Join(charge.Layers, ","); got != "payments" {
		t.Errorf("Charge layers = %q, want \"payments\"", got)
	}

	// The join is what makes the graph cross a layer boundary: both files now
	// reach each other through Address.
	sub, err := g.Subgraph(GraphOptions{RootID: address.ID, Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	var names []string
	for _, n := range sub.Nodes {
		names = append(names, n.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"libs.go", "payments.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("neighbourhood of Address = %q, want it to include %q", joined, want)
		}
	}
}

// The whole point of the guard: a name in too many layers is template text, not
// one shared symbol, and fusing it would connect everything to everything.
func TestLoadFederatedGraphSuppressesWidespreadNames(t *testing.T) {
	aiDir := t.TempDir()
	for _, layer := range []string{"a", "b", "c"} {
		seedLayer(t, aiDir, layer, nil,
			fedEntity{layer + ".md", EntityTypeFile},
			fedEntity{"Deployment", EntityTypeTopic},
		)
	}
	seedLayer(t, aiDir, "estate", []string{"a", "b", "c"},
		fedEntity{"estate.md", EntityTypeFile},
	)

	g, report := loadFed(t, aiDir, "estate", 2) // "Deployment" is in 3 layers

	var deployments int
	for _, n := range g.nodes {
		if n.Name == "Deployment" {
			deployments++
		}
	}
	if deployments != 3 {
		t.Errorf("got %d Deployment nodes, want 3 left unjoined", deployments)
	}
	if len(report.Suppressed) != 1 {
		t.Fatalf("Suppressed = %+v, want exactly one entry", report.Suppressed)
	}
	if report.Suppressed[0].Name != "Deployment" || report.Suppressed[0].Layers != 3 {
		t.Errorf("Suppressed[0] = %+v, want Deployment in 3 layers", report.Suppressed[0])
	}
	if report.Joined != 0 {
		t.Errorf("Joined = %d, want 0", report.Joined)
	}

	// Raising the guard joins it, which is the escape hatch the report offers.
	g2, report2 := loadFed(t, aiDir, "estate", 3)
	if report2.Joined != 1 {
		t.Errorf("with a raised guard, Joined = %d, want 1", report2.Joined)
	}
	nodeByName(t, g2, "Deployment") // exactly one now
}

// Node counts have to reconcile: every node the report claims a layer
// contributed must exist in the merged graph. This caught a real fusion bug —
// two layers minting the same ID silently overwrote each other, so the report
// counted a node the graph did not have.
func TestLoadFederatedGraphNodeCountsReconcile(t *testing.T) {
	aiDir := t.TempDir()
	seedLayer(t, aiDir, "a", nil,
		fedEntity{"shared.go", EntityTypeFile},
		fedEntity{"OnlyInA", EntityTypeType},
	)
	seedLayer(t, aiDir, "b", nil,
		fedEntity{"shared.go", EntityTypeFile},
		fedEntity{"OnlyInB", EntityTypeType},
	)
	seedLayer(t, aiDir, "estate", []string{"a", "b"},
		fedEntity{"estate.md", EntityTypeFile},
	)

	g, report := loadFed(t, aiDir, "estate", 0)

	claimed := 0
	for _, l := range report.Layers {
		claimed += l.Nodes
	}
	if claimed != g.NodeCount() {
		t.Errorf("layers claim %d nodes but the graph holds %d", claimed, g.NodeCount())
	}
}

// A layer that cannot be opened must degrade to a warning: a federated render
// missing one database is still useful, a silent hole is not.
func TestLoadFederatedGraphReportsUnreadableLayer(t *testing.T) {
	aiDir := t.TempDir()
	seedLayer(t, aiDir, "good", nil, fedEntity{"good.go", EntityTypeFile})
	seedLayer(t, aiDir, "estate", []string{"good", "broken"},
		fedEntity{"estate.md", EntityTypeFile},
	)
	// A scope config pointing at a database that was never created.
	if err := os.WriteFile(filepath.Join(aiDir, "scope", "broken.json"),
		[]byte(`{"name":"broken","database":"broken.db"}`), 0o644); err != nil {
		t.Fatalf("write broken scope: %v", err)
	}

	g, report := loadFed(t, aiDir, "estate", 0)

	failed := report.FailedLayers()
	if len(failed) != 1 || failed[0].Name != "broken" {
		t.Fatalf("FailedLayers = %+v, want just \"broken\"", failed)
	}
	// The readable layers still made it in.
	nodeByName(t, g, "good.go")
	nodeByName(t, g, "estate.md")
}

// seedEntityWithID inserts an entity with a chosen ID, which CreateEntity
// cannot do — it mints a UUID. Indexer-derived IDs are path-based
// ("import:..", "file:src/a.go"), so two layers indexing different repos can
// mint the same ID for different things, and that is the case under test.
func seedEntityWithID(t *testing.T, aiDir, layer, id, name, entityType string) {
	t.Helper()

	store, err := OpenStore(filepath.Join(aiDir, layer+".db"))
	if err != nil {
		t.Fatalf("OpenStore %s: %v", layer, err)
	}
	defer store.Close()

	result, err := store.QueryParams(`
		CREATE (e:Entity {id: $id, name: $name, type: $type, project_id: $project_id})
	`, map[string]any{"id": id, "name": name, "type": entityType, "project_id": fedProject})
	if err != nil {
		t.Fatalf("seed entity %s in %s: %v", id, layer, err)
	}
	result.Close()
}

// Two layers minting the same ID for different things must stay two nodes. An
// earlier version reused the ID and silently fused them — which also defeated
// the join guard, since the fusion happened whether or not the identity was
// judged joinable.
func TestLoadFederatedGraphRenamesCollidingIDs(t *testing.T) {
	aiDir := t.TempDir()
	seedLayer(t, aiDir, "a", nil, fedEntity{"a.go", EntityTypeFile})
	seedLayer(t, aiDir, "b", nil, fedEntity{"b.go", EntityTypeFile})
	seedLayer(t, aiDir, "estate", []string{"a", "b"}, fedEntity{"estate.md", EntityTypeFile})

	// The same ID in both layers, standing for genuinely different entities.
	seedEntityWithID(t, aiDir, "a", "import:..", "..", EntityTypeImport)
	seedEntityWithID(t, aiDir, "b", "import:..", "..", EntityTypeImport)

	// maxJoin 1 leaves the identity unjoined, so nothing but the ID could fuse
	// these two.
	g, report := loadFed(t, aiDir, "estate", 1)

	var dots []GraphNode
	for _, n := range g.nodes {
		if n.Name == ".." {
			dots = append(dots, n)
		}
	}
	if len(dots) != 2 {
		t.Fatalf("got %d nodes for the colliding ID, want 2 kept apart", len(dots))
	}
	if dots[0].ID == dots[1].ID {
		t.Errorf("both nodes kept ID %q", dots[0].ID)
	}
	if report.IDCollisions != 1 {
		t.Errorf("IDCollisions = %d, want 1", report.IDCollisions)
	}

	claimed := 0
	for _, l := range report.Layers {
		claimed += l.Nodes
	}
	if claimed != g.NodeCount() {
		t.Errorf("layers claim %d nodes but the graph holds %d", claimed, g.NodeCount())
	}
}

func TestFederationOrder(t *testing.T) {
	scope := &ScopeConfig{Name: "estate", Layers: []string{"a", "b", "c"}}

	// Highest priority first: the primary scope, then layers in reverse config
	// order, matching the priorities OpenFederatedStoreWithExtra assigns.
	got, err := federationOrder(FederatedGraphOptions{Scope: scope})
	if err != nil {
		t.Fatalf("federationOrder: %v", err)
	}
	if want := "estate,c,b,a"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}

	// A subset keeps that order rather than the order it was asked for.
	got, err = federationOrder(FederatedGraphOptions{Scope: scope, OnlyLayers: []string{"a", "estate"}})
	if err != nil {
		t.Fatalf("federationOrder: %v", err)
	}
	if want := "estate,a"; strings.Join(got, ",") != want {
		t.Errorf("subset order = %v, want %s", got, want)
	}

	// A name repeated in Layers, or a layer sharing the scope's own name, must
	// appear once. A duplicate would load the same database twice and then run
	// the ID-collision rename path against that database's own nodes.
	//
	// The surviving copy keeps the HIGHER-priority slot: layers are walked in
	// reverse config order, so the later listing of "a" is reached first and
	// the earlier one is skipped. "estate" as a layer collides with the scope
	// itself and drops entirely.
	dup := &ScopeConfig{Name: "estate", Layers: []string{"a", "b", "a", "estate"}}
	got, err = federationOrder(FederatedGraphOptions{Scope: dup})
	if err != nil {
		t.Fatalf("federationOrder: %v", err)
	}
	if want := "estate,a,b"; strings.Join(got, ",") != want {
		t.Errorf("deduped order = %v, want %s", got, want)
	}

	// A layer this scope does not federate with is an error, not a silent drop.
	_, err = federationOrder(FederatedGraphOptions{Scope: scope, OnlyLayers: []string{"a", "nope"}})
	if err == nil {
		t.Fatal("expected an error for a scope that is not a layer")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %q, want it to name the unknown scope", err)
	}
}

func TestLoadFederatedGraphRequiresScope(t *testing.T) {
	if _, _, err := LoadFederatedGraph(FederatedGraphOptions{}); err == nil {
		t.Error("expected an error when no scope is given")
	}
}

// A scope's Remotes are hub layers that federate into search but cannot be
// rendered: reading a layer's rows needs raw Cypher against a local *Store,
// while a remote layer is reached over HTTP. Dropping them silently would make
// --federated quietly render less than it claims, so each one is reported the
// same way an unreadable local layer is.
func TestLoadFederatedGraphReportsRemotesAsSkipped(t *testing.T) {
	aiDir := t.TempDir()
	seedLayer(t, aiDir, "estate", nil, fedEntity{name: "Root", entityType: EntityTypeFile})

	scope, err := LoadScopeConfig(aiDir, "estate")
	if err != nil {
		t.Fatalf("LoadScopeConfig: %v", err)
	}
	scope.Remotes = []string{"platform-hub", "shared-hub"}

	_, report, err := LoadFederatedGraph(FederatedGraphOptions{
		AIDir: aiDir, Scope: scope, ProjectID: fedProject,
	})
	if err != nil {
		t.Fatalf("LoadFederatedGraph: %v", err)
	}

	skipped := map[string]string{}
	for _, l := range report.FailedLayers() {
		skipped[l.Name] = l.Failed
	}
	for _, want := range []string{"remote:platform-hub", "remote:shared-hub"} {
		reason, ok := skipped[want]
		if !ok {
			t.Errorf("report does not mention %s; remotes were dropped silently", want)
			continue
		}
		if reason == "" {
			t.Errorf("%s reported with no reason", want)
		}
	}
}

// Joining is a CROSS-database rule: "two rows in two databases are the same
// node when their (name, type) match". Two distinct entities inside ONE layer
// that happen to share a name must stay distinct — nothing dedupes (name,
// type) within a database, since CreateEntity mints a fresh UUID per row, so a
// Go codebase with an internal/api/Config and an internal/worker/Config is
// ordinary rather than exotic.
//
// The hazard is that such a pair only fuses when the name ALSO appears in
// another layer, which is what makes the key joinable in the first place.
func TestLoadFederatedGraphKeepsSameLayerNamesakesApart(t *testing.T) {
	aiDir := t.TempDir()
	// Two unrelated Config types in one layer, plus a Config in another so the
	// (name, type) key counts as joinable.
	seedLayer(t, aiDir, "payments", []string{"libs"},
		fedEntity{name: "Root", entityType: EntityTypeFile},
		fedEntity{name: "Config", entityType: EntityTypeType},
		fedEntity{name: "Config", entityType: EntityTypeType})
	seedLayer(t, aiDir, "libs", nil,
		fedEntity{name: "Config", entityType: EntityTypeType})

	g, _ := loadFed(t, aiDir, "payments", 0)

	configs := 0
	for _, n := range g.nodes {
		if n.Name == "Config" {
			configs++
		}
	}
	// payments' two stay separate; libs' joins onto one of them.
	if configs != 2 {
		t.Errorf("Config nodes = %d, want 2 — the two same-layer Configs were fused into one", configs)
	}
}
