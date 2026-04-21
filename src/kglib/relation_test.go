package kglib

import (
	"testing"
)

func TestCreateRelation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create two entities
	entity1, err := store.CreateEntity("func1", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	entity2, err := store.CreateEntity("func2", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Create relation
	err = store.CreateRelation(entity1.ID, entity2.ID, "CALLS", "proj1")
	if err != nil {
		t.Fatalf("CreateRelation failed: %v", err)
	}
}

func TestCreateRelationInvalidType(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity1, err := store.CreateEntity("func1", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	entity2, err := store.CreateEntity("func2", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Try invalid relation type
	err = store.CreateRelation(entity1.ID, entity2.ID, "INVALID", "proj1")
	if err == nil {
		t.Error("Expected error for invalid relation type")
	}
}

func TestCreateRelationNonExistentEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, err := store.CreateEntity("func1", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Try to create relation to non-existent entity
	err = store.CreateRelation(entity.ID, "non-existent", "CALLS", "proj1")
	if err == nil {
		t.Error("Expected error for non-existent target entity")
	}
}

func TestGetRelations(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entities
	entity1, _ := store.CreateEntity("func1", "function", "proj1")
	entity2, _ := store.CreateEntity("func2", "function", "proj1")
	entity3, _ := store.CreateEntity("func3", "function", "proj1")

	// Create relations
	store.CreateRelation(entity1.ID, entity2.ID, "CALLS", "proj1")
	store.CreateRelation(entity1.ID, entity3.ID, "CALLS", "proj1")

	// Get relations
	relations, err := store.GetRelations(entity1.ID, "proj1")
	if err != nil {
		t.Fatalf("GetRelations failed: %v", err)
	}

	if len(relations) != 2 {
		t.Errorf("Expected 2 relations, got %d", len(relations))
	}

	for _, rel := range relations {
		if rel.FromID != entity1.ID {
			t.Errorf("Expected FromID %s, got %s", entity1.ID, rel.FromID)
		}
		if rel.Type != "CALLS" {
			t.Errorf("Expected type CALLS, got %s", rel.Type)
		}
	}
}

func TestDeleteRelation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity1, _ := store.CreateEntity("func1", "function", "proj1")
	entity2, _ := store.CreateEntity("func2", "function", "proj1")

	// Create relation
	store.CreateRelation(entity1.ID, entity2.ID, "CALLS", "proj1")

	// Verify it exists
	relations, _ := store.GetRelations(entity1.ID, "proj1")
	if len(relations) != 1 {
		t.Fatalf("Expected 1 relation before delete, got %d", len(relations))
	}

	// Delete relation
	err := store.DeleteRelation(entity1.ID, entity2.ID, "CALLS", "proj1")
	if err != nil {
		t.Fatalf("DeleteRelation failed: %v", err)
	}

	// Verify it's deleted
	relations, _ = store.GetRelations(entity1.ID, "proj1")
	if len(relations) != 0 {
		t.Errorf("Expected 0 relations after delete, got %d", len(relations))
	}
}

func TestTraverseRelations(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create a call chain: func1 -> func2 -> func3
	func1, _ := store.CreateEntity("func1", "function", "proj1")
	func2, _ := store.CreateEntity("func2", "function", "proj1")
	func3, _ := store.CreateEntity("func3", "function", "proj1")

	store.CreateRelation(func1.ID, func2.ID, "CALLS", "proj1")
	store.CreateRelation(func1.ID, func3.ID, "CALLS", "proj1")

	// Traverse CALLS from func1
	targets, err := store.TraverseRelations(func1.ID, "CALLS", "proj1")
	if err != nil {
		t.Fatalf("TraverseRelations failed: %v", err)
	}

	if len(targets) != 2 {
		t.Errorf("Expected 2 target entities, got %d", len(targets))
	}

	// Verify we got func2 and func3
	names := make(map[string]bool)
	for _, entity := range targets {
		names[entity.Name] = true
	}

	if !names["func2"] || !names["func3"] {
		t.Error("Expected to find func2 and func3 in traversal")
	}
}

func TestTraverseRelationsProjectIsolation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entities in project A
	func1a, _ := store.CreateEntity("func1", "function", "project-a")
	func2a, _ := store.CreateEntity("func2", "function", "project-a")

	// Create entities in project B
	func1b, _ := store.CreateEntity("func1", "function", "project-b")
	func2b, _ := store.CreateEntity("func2", "function", "project-b")

	// Create relations in both projects
	store.CreateRelation(func1a.ID, func2a.ID, "CALLS", "project-a")
	store.CreateRelation(func1b.ID, func2b.ID, "CALLS", "project-b")

	// Traverse from project A
	targetsA, _ := store.TraverseRelations(func1a.ID, "CALLS", "project-a")
	if len(targetsA) != 1 {
		t.Errorf("Expected 1 target in project-a, got %d", len(targetsA))
	}
	if targetsA[0].Name != "func2" || targetsA[0].ProjectID != "project-a" {
		t.Error("Got wrong entity from project-a traversal")
	}

	// Traverse from project B
	targetsB, _ := store.TraverseRelations(func1b.ID, "CALLS", "project-b")
	if len(targetsB) != 1 {
		t.Errorf("Expected 1 target in project-b, got %d", len(targetsB))
	}
	if targetsB[0].Name != "func2" || targetsB[0].ProjectID != "project-b" {
		t.Error("Got wrong entity from project-b traversal")
	}
}
