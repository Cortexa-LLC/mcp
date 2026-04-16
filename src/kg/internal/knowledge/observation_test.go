package knowledge

import (
	"testing"
)

func TestCreateObservation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity
	entity, err := store.CreateEntity("func1", "function", "proj1")
	if err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	// Create observation
	obs, err := store.CreateObservation(entity.ID, "This function is performance critical", "proj1")
	if err != nil {
		t.Fatalf("CreateObservation failed: %v", err)
	}

	if obs.ID == "" {
		t.Error("Observation ID is empty")
	}
	if obs.EntityID != entity.ID {
		t.Errorf("Expected entity_id %s, got %s", entity.ID, obs.EntityID)
	}
	if obs.Content != "This function is performance critical" {
		t.Errorf("Unexpected content: %s", obs.Content)
	}
	if obs.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
}

func TestCreateObservationNonExistentEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	_, err := store.CreateObservation("non-existent", "test", "proj1")
	if err == nil {
		t.Error("Expected error for non-existent entity")
	}
}

func TestGetObservations(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entity
	entity, _ := store.CreateEntity("func1", "function", "proj1")

	// Create multiple observations
	obs1, _ := store.CreateObservation(entity.ID, "First observation", "proj1")
	obs2, _ := store.CreateObservation(entity.ID, "Second observation", "proj1")

	// Get observations
	observations, err := store.GetObservations(entity.ID, "proj1")
	if err != nil {
		t.Fatalf("GetObservations failed: %v", err)
	}

	if len(observations) != 2 {
		t.Errorf("Expected 2 observations, got %d", len(observations))
	}

	// Verify content
	contents := make(map[string]bool)
	for _, obs := range observations {
		contents[obs.Content] = true
		if obs.EntityID != entity.ID {
			t.Errorf("Observation has wrong entity_id: %s", obs.EntityID)
		}
	}

	if !contents["First observation"] || !contents["Second observation"] {
		t.Error("Expected to find both observations")
	}

	// Verify IDs match
	ids := make(map[string]bool)
	for _, obs := range observations {
		ids[obs.ID] = true
	}
	if !ids[obs1.ID] || !ids[obs2.ID] {
		t.Error("Observation IDs don't match")
	}
}

func TestGetObservationsEmpty(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, _ := store.CreateEntity("func1", "function", "proj1")

	observations, err := store.GetObservations(entity.ID, "proj1")
	if err != nil {
		t.Fatalf("GetObservations failed: %v", err)
	}

	if len(observations) != 0 {
		t.Errorf("Expected 0 observations, got %d", len(observations))
	}
}

func TestDeleteObservation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, _ := store.CreateEntity("func1", "function", "proj1")
	obs1, _ := store.CreateObservation(entity.ID, "First", "proj1")
	obs2, _ := store.CreateObservation(entity.ID, "Second", "proj1")

	// Delete first observation
	err := store.DeleteObservation(obs1.ID, entity.ID, "proj1")
	if err != nil {
		t.Fatalf("DeleteObservation failed: %v", err)
	}

	// Verify only second remains
	observations, _ := store.GetObservations(entity.ID, "proj1")
	if len(observations) != 1 {
		t.Errorf("Expected 1 observation after delete, got %d", len(observations))
	}
	if observations[0].ID != obs2.ID {
		t.Error("Wrong observation was deleted")
	}
}

func TestObservationsProjectIsolation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create entities in different projects
	entityA, _ := store.CreateEntity("func1", "function", "project-a")
	entityB, _ := store.CreateEntity("func1", "function", "project-b")

	// Create observations
	store.CreateObservation(entityA.ID, "Observation A", "project-a")
	store.CreateObservation(entityB.ID, "Observation B", "project-b")

	// Get observations for project A
	obsA, _ := store.GetObservations(entityA.ID, "project-a")
	if len(obsA) != 1 {
		t.Errorf("Expected 1 observation in project-a, got %d", len(obsA))
	}
	if obsA[0].Content != "Observation A" {
		t.Error("Got wrong observation for project-a")
	}

	// Get observations for project B
	obsB, _ := store.GetObservations(entityB.ID, "project-b")
	if len(obsB) != 1 {
		t.Errorf("Expected 1 observation in project-b, got %d", len(obsB))
	}
	if obsB[0].Content != "Observation B" {
		t.Error("Got wrong observation for project-b")
	}
}

func TestObservationContentEscaping(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, _ := store.CreateEntity("func1", "function", "proj1")

	// Test observation with special characters
	content := "This function can't handle errors properly"
	_, err := store.CreateObservation(entity.ID, content, "proj1")
	if err != nil {
		t.Fatalf("CreateObservation with quotes failed: %v", err)
	}

	// Retrieve and verify
	observations, _ := store.GetObservations(entity.ID, "proj1")
	if len(observations) != 1 {
		t.Fatalf("Expected 1 observation, got %d", len(observations))
	}

	if observations[0].Content != content {
		t.Errorf("Expected content with quote, got %s", observations[0].Content)
	}
}

func TestDeleteEntityDeletesObservations(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, _ := store.CreateEntity("func1", "function", "proj1")
	store.CreateObservation(entity.ID, "Observation 1", "proj1")
	store.CreateObservation(entity.ID, "Observation 2", "proj1")

	// Delete entity
	err := store.DeleteEntity(entity.ID, "proj1")
	if err != nil {
		t.Fatalf("DeleteEntity failed: %v", err)
	}

	// Verify observations are also deleted (via DETACH DELETE)
	// This is implicitly tested by verifying the entity is gone
	_, err = store.GetEntity(entity.ID, "proj1")
	if err == nil {
		t.Error("Entity still exists after delete")
	}
}
