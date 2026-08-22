package kglib

import (
	"testing"
	"time"
)

// SetMeta then GetMeta returns the stamp unchanged.
func TestMeta_RoundTrip(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	want := KGMeta{
		ProjectID:  "proj",
		RepoURL:    "git@github.com:cortexa-llc/mcp.git",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Dirty:      true,
		IndexedAt:  time.Now().UTC().Truncate(time.Microsecond),
		EmbedModel: "text-embedding-3-small",
		KGVersion:  "1.2.3",
	}
	if err := store.SetMeta(want); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	got, err := store.GetMeta("proj")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got == nil {
		t.Fatal("GetMeta returned nil after SetMeta")
	}
	if got.ProjectID != want.ProjectID || got.RepoURL != want.RepoURL ||
		got.Commit != want.Commit || got.Dirty != want.Dirty ||
		got.EmbedModel != want.EmbedModel || got.KGVersion != want.KGVersion {
		t.Errorf("GetMeta mismatch: got %+v, want %+v", got, want)
	}
	// Kuzu stores timestamps at microsecond precision.
	if !got.IndexedAt.Equal(want.IndexedAt) {
		t.Errorf("IndexedAt mismatch: got %v, want %v", got.IndexedAt, want.IndexedAt)
	}
}

// A second SetMeta replaces the first — exactly one row per project.
func TestMeta_Upsert(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	first := KGMeta{ProjectID: "proj", Commit: "aaaa", IndexedAt: time.Now().UTC()}
	second := KGMeta{ProjectID: "proj", Commit: "bbbb", IndexedAt: time.Now().UTC()}
	if err := store.SetMeta(first); err != nil {
		t.Fatalf("SetMeta (first): %v", err)
	}
	if err := store.SetMeta(second); err != nil {
		t.Fatalf("SetMeta (second): %v", err)
	}

	got, err := store.GetMeta("proj")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got == nil || got.Commit != "bbbb" {
		t.Fatalf("expected the second stamp's commit, got %+v", got)
	}

	result, err := store.Query("MATCH (m:KGMeta) RETURN count(*)")
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	defer result.Close()
	if !result.HasNext() {
		t.Fatal("count query returned no rows")
	}
	tuple, err := result.Next()
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	defer tuple.Close()
	row, err := tuple.GetAsSlice()
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if count, ok := row[0].(int64); !ok || count != 1 {
		t.Errorf("expected exactly 1 KGMeta row, got %v", row[0])
	}
}

// A fresh store has no stamp: (nil, nil), not an error.
func TestMeta_MissingStamp(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	got, err := store.GetMeta("proj")
	if err != nil {
		t.Fatalf("GetMeta on fresh store: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil meta on fresh store, got %+v", got)
	}
}
