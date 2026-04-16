package knowledge

import (
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	return store
}

func TestCreateEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, err := store.CreateEntity("main.go", "file", "test-project")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	if entity.ID == "" {
		t.Error("Entity ID is empty")
	}
	if entity.Name != "main.go" {
		t.Errorf("Expected name 'main.go', got '%s'", entity.Name)
	}
	if entity.Type != "file" {
		t.Errorf("Expected type 'file', got '%s'", entity.Type)
	}
	if entity.ProjectID != "test-project" {
		t.Errorf("Expected project_id 'test-project', got '%s'", entity.ProjectID)
	}
	if entity.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
	if entity.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
}

func TestGetEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity
	created, err := store.CreateEntity("test.go", "file", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Retrieve entity
	retrieved, err := store.GetEntity(created.ID, "proj1")
	if err != nil {
		t.Fatalf("GetEntity failed: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("Expected ID %s, got %s", created.ID, retrieved.ID)
	}
	if retrieved.Name != created.Name {
		t.Errorf("Expected name %s, got %s", created.Name, retrieved.Name)
	}
	if retrieved.Type != created.Type {
		t.Errorf("Expected type %s, got %s", created.Type, retrieved.Type)
	}
}

func TestGetEntityNotFound(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	_, err := store.GetEntity("non-existent-id", "proj1")
	if err == nil {
		t.Error("Expected error for non-existent entity")
	}
}

func TestGetEntityWrongProject(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity in project A
	entity, err := store.CreateEntity("test.go", "file", "project-a")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Try to retrieve from project B
	_, err = store.GetEntity(entity.ID, "project-b")
	if err == nil {
		t.Error("Expected error when accessing entity from different project")
	}
}

func TestListEntities(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entities in project A
	_, err := store.CreateEntity("file1.go", "file", "project-a")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}
	_, err = store.CreateEntity("file2.go", "file", "project-a")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}
	_, err = store.CreateEntity("func1", "function", "project-a")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Create entity in project B
	_, err = store.CreateEntity("file3.go", "file", "project-b")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// List all entities in project A
	entities, err := store.ListEntities("project-a", "")
	if err != nil {
		t.Fatalf("ListEntities failed: %v", err)
	}
	if len(entities) != 3 {
		t.Errorf("Expected 3 entities in project-a, got %d", len(entities))
	}

	// List only files in project A
	files, err := store.ListEntities("project-a", "file")
	if err != nil {
		t.Fatalf("ListEntities failed: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("Expected 2 file entities in project-a, got %d", len(files))
	}
}

func TestDeleteEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity
	entity, err := store.CreateEntity("test.go", "file", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Delete entity
	err = store.DeleteEntity(entity.ID, "proj1")
	if err != nil {
		t.Fatalf("DeleteEntity failed: %v", err)
	}

	// Verify entity is deleted
	_, err = store.GetEntity(entity.ID, "proj1")
	if err == nil {
		t.Error("Expected error after deleting entity")
	}
}

func TestDeleteEntityWrongProject(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity in project A
	entity, err := store.CreateEntity("test.go", "file", "project-a")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Try to delete from project B
	err = store.DeleteEntity(entity.ID, "project-b")
	if err == nil {
		t.Error("Expected error when deleting entity from different project")
	}

	// Verify entity still exists in project A
	_, err = store.GetEntity(entity.ID, "project-a")
	if err != nil {
		t.Error("Entity was deleted from wrong project")
	}
}

func TestEntityNameEscaping(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Test entity name with special characters
	entity, err := store.CreateEntity("it's a test", "file", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity with quotes failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetEntity(entity.ID, "proj1")
	if err != nil {
		t.Fatalf("GetEntity failed: %v", err)
	}

	if retrieved.Name != "it's a test" {
		t.Errorf("Expected name with quote, got %s", retrieved.Name)
	}
}
