package kglib

import (
	"path/filepath"
	"testing"
)

// TestAllRelationshipTypes verifies all relationship types defined in the schema work correctly
func TestAllRelationshipTypes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()

	// Create two test entities
	entity1, err := store.CreateEntity("Source", "test", "test-project")
	if err != nil {
		t.Fatalf("Failed to create entity1: %v", err)
	}

	entity2, err := store.CreateEntity("Target", "test", "test-project")
	if err != nil {
		t.Fatalf("Failed to create entity2: %v", err)
	}

	// Test all relationship types from the schema
	relationTypes := []string{
		"CALLS",
		"IMPORTS",
		"FIXES",
		"SUPERSEDES",
		"CAUSED_BY",
		"DEPENDS_ON",
		"IMPLEMENTS",
		"RELATES_TO",
		"TESTS",
		"DOCUMENTS",
	}

	for _, relType := range relationTypes {
		t.Run(relType, func(t *testing.T) {
			// Create relation
			err := store.CreateRelation(entity1.ID, entity2.ID, relType, "test-project")
			if err != nil {
				t.Fatalf("Failed to create %s relation: %v", relType, err)
			}

			// Verify relation exists via traversal
			targets, err := store.TraverseRelations(entity1.ID, relType, "test-project")
			if err != nil {
				t.Fatalf("Failed to traverse %s relation: %v", relType, err)
			}

			if len(targets) != 1 {
				t.Fatalf("Expected 1 target, got %d", len(targets))
			}

			if targets[0].ID != entity2.ID {
				t.Errorf("Expected target %s, got %s", entity2.ID, targets[0].ID)
			}

			t.Logf("✓ %s relation works correctly", relType)
		})
	}
}
