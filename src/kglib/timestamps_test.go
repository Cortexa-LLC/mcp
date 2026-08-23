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
