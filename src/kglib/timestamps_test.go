package kglib

import (
	"testing"
	"time"
)

// Kuzu returns TIMESTAMP columns as time.Time. Asserting int64 microseconds
// instead used to fail silently, leaving every timestamp zero — which also
// disabled the recency component of hybrid search, since calculateRecencyScore
// treats a zero time as "no boost". These tests pin the round trip so a driver
// change cannot quietly reintroduce that.
func TestTimeOrZero_AcceptsDriverAndMicrosecondForms(t *testing.T) {
	want := time.Date(2026, 8, 18, 10, 30, 0, 0, time.UTC)

	if got := timeOrZero(want); !got.Equal(want) {
		t.Errorf("time.Time form: got %v, want %v", got, want)
	}
	if got := timeOrZero(want.UnixMicro()); !got.Equal(want) {
		t.Errorf("int64 microsecond form: got %v, want %v", got, want)
	}
	if got := timeOrZero("not a timestamp"); !got.IsZero() {
		t.Errorf("unknown form should be zero, got %v", got)
	}
	if got := timeOrZero(nil); !got.IsZero() {
		t.Errorf("nil should be zero, got %v", got)
	}
}

func TestEntityTimestamps_SurviveRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	created, err := store.CreateEntity("timestampedEntity", "function", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("CreateEntity returned a zero CreatedAt")
	}

	got, err := store.GetEntity(created.ID, "proj")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("GetEntity lost timestamps: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}

	listed, err := store.ListEntities("proj", "")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("ListEntities returned nothing")
	}
	for _, e := range listed {
		if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
			t.Errorf("ListEntities lost timestamps for %s", e.Name)
		}
	}

	byName, err := store.GetEntityByName("timestampedEntity", "proj")
	if err != nil {
		t.Fatalf("GetEntityByName: %v", err)
	}
	if byName != nil && byName.CreatedAt.IsZero() {
		t.Error("GetEntityByName lost CreatedAt")
	}
}

func TestObservationAndTraversalTimestamps_SurviveRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	from, err := store.CreateEntity("fromEntity", "function", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	to, err := store.CreateEntity("toEntity", "function", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(from.ID, "an observation", "proj"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.CreateRelation(from.ID, to.ID, "CALLS", "proj"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	observations, err := store.GetObservations(from.ID, "proj")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) == 0 {
		t.Fatal("no observations returned")
	}
	for _, o := range observations {
		if o.CreatedAt.IsZero() {
			t.Error("GetObservations lost CreatedAt")
		}
	}

	// Relation carries no timestamps; TraverseRelations hydrates entities, which do.
	traversed, err := store.TraverseRelations(from.ID, "CALLS", "proj")
	if err != nil {
		t.Fatalf("TraverseRelations: %v", err)
	}
	if len(traversed) == 0 {
		t.Fatal("no entities returned from traversal")
	}
	for _, e := range traversed {
		if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
			t.Errorf("TraverseRelations lost timestamps for %s", e.Name)
		}
	}
}

// assertRecent fails unless ts falls inside the window that started just
// before the fixture was created. "Non-zero" alone would accept a scan that
// invents a wrong-but-present value; a freshly written row must read back as
// written seconds ago.
func assertRecent(t *testing.T, what string, ts time.Time, notBefore time.Time) {
	t.Helper()
	if ts.IsZero() {
		t.Errorf("%s is the zero time — the read path dropped the stored timestamp", what)
		return
	}
	if ts.Before(notBefore) || ts.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("%s = %v, want within the last minute (not before %v)", what, ts, notBefore)
	}
}

// A row written moments ago must read back with a CreatedAt inside the last
// minute on EVERY retrieval path an agent uses: the direct gets and the
// keyword search that feeds search_knowledge — including the observations
// attached to each search result, which come through batchGetObservations.
// Against the old int64 scan every one of these read as the zero time.
func TestFreshWritesReadBackRecentOnGetAndSearchPaths(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	notBefore := time.Now().UTC().Add(-time.Minute)

	entity, err := store.CreateEntity("freshTimestampProbe", "function", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "freshTimestampProbe observation", "proj"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	// Direct get paths.
	got, err := store.GetEntity(entity.ID, "proj")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	assertRecent(t, "GetEntity CreatedAt", got.CreatedAt, notBefore)
	assertRecent(t, "GetEntity UpdatedAt", got.UpdatedAt, notBefore)

	observations, err := store.GetObservations(entity.ID, "proj")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) == 0 {
		t.Fatal("no observations returned")
	}
	for _, o := range observations {
		assertRecent(t, "GetObservations CreatedAt", o.CreatedAt, notBefore)
	}

	// Search path — the one search_knowledge serves.
	results, err := store.KeywordSearch("proj", "freshTimestampProbe", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected a search hit")
	}
	sawObservation := false
	for _, r := range results {
		assertRecent(t, "search result entity CreatedAt", r.Entity.CreatedAt, notBefore)
		assertRecent(t, "search result entity UpdatedAt", r.Entity.UpdatedAt, notBefore)
		for _, o := range r.Observations {
			sawObservation = true
			assertRecent(t, "search result observation CreatedAt", o.CreatedAt, notBefore)
		}
	}
	if !sawObservation {
		t.Fatal("search results carried no observations — the observation-timestamp path was not exercised")
	}
}

// Search results must carry timestamps, otherwise the recency weight in
// SearchConfig has no effect on ranking.
func TestSearchResults_CarryTimestampsForRecencyScoring(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	if _, err := store.CreateEntity("recencyProbe", "function", "proj"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	results, err := store.KeywordSearch("proj", "recencyProbe", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected a search hit")
	}
	if results[0].Entity.UpdatedAt.IsZero() {
		t.Fatal("search result has a zero UpdatedAt — recency scoring is inert")
	}
	if score := calculateRecencyScore(results[0].Entity.UpdatedAt, time.Now().UTC()); score <= 0 {
		t.Errorf("a just-created entity should get a recency boost, got %f", score)
	}
}
