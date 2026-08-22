package kglib

import (
	"path/filepath"
	"testing"
	"time"
)

// newLayerStore creates an independent store to act as one federation layer.
func newLayerStore(t *testing.T, name string) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), name+".db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("open layer %s: %v", name, err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func mustEntity(t *testing.T, s *Store, name, entityType, projectID string) *Entity {
	t.Helper()
	e, err := s.CreateEntity(name, entityType, projectID)
	if err != nil {
		t.Fatalf("create entity %s: %v", name, err)
	}
	return e
}

// insertEntityWithID inserts an entity with a caller-chosen ID, mirroring the
// deterministic IDs the kg indexer assigns (e.g. "function:path/file.go:Name").
// CreateEntity assigns a random UUID instead, so it cannot produce the
// cross-layer ID collisions that priority merging exists to resolve.
func insertEntityWithID(t *testing.T, s *Store, id, name, entityType, projectID string) {
	t.Helper()
	now := time.Now().UTC()
	result, err := s.QueryParams(`
		CREATE (e:Entity {
			id: $id, name: $name, type: $type, project_id: $project_id,
			created_at: $created_at, updated_at: $updated_at
		})
	`, map[string]any{
		"id": id, "name": name, "type": entityType, "project_id": projectID,
		"created_at": now, "updated_at": now,
	})
	if err != nil {
		t.Fatalf("insert entity %s: %v", id, err)
	}
	result.Close()
}

func resultNames(results []*SearchResult) []string {
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Entity.Name)
	}
	return names
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// A federated search returns entities from every layer.
func TestFederatedStore_SearchesAllLayers(t *testing.T) {
	base := newLayerStore(t, "base")
	primary := newLayerStore(t, "primary")

	mustEntity(t, base, "platformAuthHelper", "function", "proj")
	mustEntity(t, primary, "teamAuthHandler", "function", "proj")

	fs := NewFederatedStore([]LayerConfig{
		{Name: "base", Store: base, Priority: 1},
		{Name: "primary", Store: primary, Priority: 11},
	})

	results, err := fs.KeywordSearch("proj", "auth", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}

	names := resultNames(results)
	if !contains(names, "platformAuthHelper") || !contains(names, "teamAuthHandler") {
		t.Fatalf("expected entities from both layers, got %v", names)
	}
}

// The highest-priority layer wins for an entity present in several layers.
func TestFederatedStore_HigherPriorityWins(t *testing.T) {
	base := newLayerStore(t, "base")
	primary := newLayerStore(t, "primary")

	// Same entity ID in both layers, as happens when two scopes index the same file.
	const sharedID = "function:internal/auth/token.go:validateAuthToken"
	insertEntityWithID(t, base, sharedID, "validateAuthToken", "function", "proj")
	insertEntityWithID(t, primary, sharedID, "validateAuthToken", "function", "proj")

	if _, err := primary.CreateObservation(sharedID, "team-specific detail", "proj"); err != nil {
		t.Fatalf("create observation: %v", err)
	}

	fs := NewFederatedStore([]LayerConfig{
		{Name: "base", Store: base, Priority: 1},
		{Name: "primary", Store: primary, Priority: 11},
	})

	results, err := fs.KeywordSearch("proj", "validateAuthToken", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}

	var matches int
	for _, r := range results {
		if r.Entity.ID != sharedID {
			continue
		}
		matches++
		if len(r.Observations) == 0 {
			t.Error("expected the higher-priority layer's version (with its observation) to win")
		}
	}
	if matches != 1 {
		t.Fatalf("expected the duplicate entity exactly once after merging, got %d", matches)
	}
}

// Two layers at equal priority holding the same entity have their scores summed,
// which ranks a cross-layer hit above a single-layer one.
func TestFederatedStore_EqualPrioritySumsScores(t *testing.T) {
	left := newLayerStore(t, "left")
	right := newLayerStore(t, "right")

	const sharedID = "function:internal/auth/token.go:authShared"
	insertEntityWithID(t, left, sharedID, "authShared", "function", "proj")
	insertEntityWithID(t, right, sharedID, "authShared", "function", "proj")

	single := NewFederatedStore([]LayerConfig{
		{Name: "left", Store: left, Priority: 5},
	})
	singleResults, err := single.KeywordSearch("proj", "authShared", 20)
	if err != nil {
		t.Fatalf("KeywordSearch (single): %v", err)
	}
	if len(singleResults) != 1 {
		t.Fatalf("expected 1 result from one layer, got %d", len(singleResults))
	}

	both := NewFederatedStore([]LayerConfig{
		{Name: "left", Store: left, Priority: 5},
		{Name: "right", Store: right, Priority: 5},
	})
	bothResults, err := both.KeywordSearch("proj", "authShared", 20)
	if err != nil {
		t.Fatalf("KeywordSearch (both): %v", err)
	}
	if len(bothResults) != 1 {
		t.Fatalf("expected the duplicate merged into 1 result, got %d", len(bothResults))
	}
	if bothResults[0].Score <= singleResults[0].Score {
		t.Errorf("expected summed score above single-layer score: %f vs %f",
			bothResults[0].Score, singleResults[0].Score)
	}
}

// A per-layer ProjectID lets a layer that files entities under its own project
// ID participate in a search scoped to a different project — the case a
// user-global personal graph needs.
func TestFederatedStore_PerLayerProjectID(t *testing.T) {
	personal := newLayerStore(t, "personal")
	project := newLayerStore(t, "project")

	mustEntity(t, personal, "authRetroLearning", "learning", "personal")
	mustEntity(t, project, "authMiddleware", "function", "my-repo")

	// Without the override the personal layer contributes nothing.
	plain := NewFederatedStore([]LayerConfig{
		{Name: "personal", Store: personal, Priority: 1},
		{Name: "project", Store: project, Priority: 11},
	})
	results, err := plain.KeywordSearch("my-repo", "auth", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if contains(resultNames(results), "authRetroLearning") {
		t.Fatal("personal entity leaked into a differently-scoped search without an override")
	}

	// With the override it does.
	withOverride := NewFederatedStore([]LayerConfig{
		{Name: "personal", Store: personal, Priority: 1, ProjectID: "personal"},
		{Name: "project", Store: project, Priority: 11},
	})
	results, err = withOverride.KeywordSearch("my-repo", "auth", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	names := resultNames(results)
	if !contains(names, "authRetroLearning") {
		t.Errorf("expected the personal layer's entity via ProjectID override, got %v", names)
	}
	if !contains(names, "authMiddleware") {
		t.Errorf("expected the project layer's entity, got %v", names)
	}
}

// The merged result set honours the configured limit.
func TestFederatedStore_AppliesLimit(t *testing.T) {
	base := newLayerStore(t, "base")
	primary := newLayerStore(t, "primary")

	for _, n := range []string{"authOne", "authTwo", "authThree"} {
		mustEntity(t, base, n, "function", "proj")
	}
	for _, n := range []string{"authFour", "authFive"} {
		mustEntity(t, primary, n, "function", "proj")
	}

	fs := NewFederatedStore([]LayerConfig{
		{Name: "base", Store: base, Priority: 1},
		{Name: "primary", Store: primary, Priority: 11},
	})

	results, err := fs.HybridSearch("proj", "auth", nil, SearchConfig{Limit: 2, KeywordWeight: 0.4})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) > 2 {
		t.Fatalf("expected at most 2 results, got %d (%v)", len(results), resultNames(results))
	}
}

// Results come back sorted by score, descending.
func TestFederatedStore_SortsByScoreDescending(t *testing.T) {
	base := newLayerStore(t, "base")
	primary := newLayerStore(t, "primary")

	mustEntity(t, base, "authHandler", "function", "proj")
	mustEntity(t, primary, "authHandlerFactoryBuilder", "function", "proj")

	fs := NewFederatedStore([]LayerConfig{
		{Name: "base", Store: base, Priority: 1},
		{Name: "primary", Store: primary, Priority: 11},
	})

	results, err := fs.KeywordSearch("proj", "authHandler", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Fatalf("results not sorted by score: %f before %f", results[i-1].Score, results[i].Score)
		}
	}
}

// A failing layer degrades the search rather than failing it.
func TestFederatedStore_SurvivesFailingLayer(t *testing.T) {
	good := newLayerStore(t, "good")
	broken := newLayerStore(t, "broken")

	mustEntity(t, good, "authHandler", "function", "proj")

	// Break the layer by removing the table its queries depend on. (Closing the
	// store instead would crash: Kuzu's C layer segfaults on use-after-close
	// rather than returning an error.)
	for _, relType := range append([]string{"HAS_OBSERVATION"}, testRelTypes...) {
		if _, err := broken.Query("DROP TABLE " + relType); err != nil {
			t.Fatalf("drop rel table %s: %v", relType, err)
		}
	}
	if _, err := broken.Query("DROP TABLE Entity"); err != nil {
		t.Fatalf("drop entity table: %v", err)
	}

	fs := NewFederatedStore([]LayerConfig{
		{Name: "broken", Store: broken, Priority: 1},
		{Name: "good", Store: good, Priority: 11},
	})

	results, err := fs.KeywordSearch("proj", "auth", 20)
	if err != nil {
		t.Fatalf("a failing layer must not fail the search: %v", err)
	}
	if !contains(resultNames(results), "authHandler") {
		t.Fatalf("expected the healthy layer's results, got %v", resultNames(results))
	}
}

// fakeLayer is a SearchLayer backed by canned results, exercising federation
// with a non-*Store layer the way a future remote hub layer would plug in.
type fakeLayer struct{ results []*SearchResult }

func (f *fakeLayer) HybridSearch(projectID, query string, emb []float32, cfg SearchConfig) ([]*SearchResult, error) {
	return f.results, nil
}

func (f *fakeLayer) Close() error { return nil }

func fakeResult(id string, score float64) *SearchResult {
	return &SearchResult{
		Entity:    &Entity{ID: id, Name: id},
		Score:     score,
		MatchType: "keyword",
	}
}

// A custom SearchLayer implementation participates in merging exactly like a
// local store: higher priority wins for duplicates, equal priorities sum
// scores, and the merged set is sorted by score descending.
func TestFederatedStoreCustomSearchLayer(t *testing.T) {
	low := &fakeLayer{results: []*SearchResult{
		fakeResult("function:auth.go:validate", 0.9),
		fakeResult("function:auth.go:shared", 0.3),
	}}
	// Same priority as `low`, sharing one entity with it → scores sum.
	peer := &fakeLayer{results: []*SearchResult{
		fakeResult("function:auth.go:shared", 0.3),
	}}
	// Higher priority, duplicating `validate` → its result wins outright.
	high := &fakeLayer{results: []*SearchResult{
		fakeResult("function:auth.go:validate", 0.2),
	}}

	fs := NewFederatedStore([]LayerConfig{
		{Name: "low", Store: low, Priority: 1},
		{Name: "peer", Store: peer, Priority: 1},
		{Name: "high", Store: high, Priority: 2},
	})

	results, err := fs.KeywordSearch("proj", "auth", 20)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 merged results, got %d (%v)", len(results), resultNames(results))
	}

	byID := make(map[string]*SearchResult, len(results))
	for _, r := range results {
		byID[r.Entity.ID] = r
	}

	// Duplicate across different priorities: the higher-priority layer's
	// result (score 0.2) replaces the lower-priority one (0.9).
	if got := byID["function:auth.go:validate"].Score; got != 0.2 {
		t.Errorf("expected higher-priority layer's score 0.2 for validate, got %f", got)
	}
	// Duplicate at equal priority: scores sum (0.3 + 0.3).
	if got := byID["function:auth.go:shared"].Score; got != 0.6 {
		t.Errorf("expected summed score 0.6 for shared, got %f", got)
	}

	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Fatalf("results not sorted by score: %f before %f", results[i-1].Score, results[i].Score)
		}
	}
}

// PrimaryStore returns the highest-priority (last) layer, and nil when empty.
func TestFederatedStore_PrimaryStore(t *testing.T) {
	base := newLayerStore(t, "base")
	primary := newLayerStore(t, "primary")

	fs := NewFederatedStore([]LayerConfig{
		{Name: "base", Store: base, Priority: 1},
		{Name: "primary", Store: primary, Priority: 11},
	})
	if got := fs.PrimaryStore(); got != primary {
		t.Error("PrimaryStore should return the last layer's store")
	}

	if got := NewFederatedStore(nil).PrimaryStore(); got != nil {
		t.Error("PrimaryStore on an empty federation should be nil")
	}
}
