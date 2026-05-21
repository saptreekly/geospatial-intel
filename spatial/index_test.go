package spatial

import (
	"fmt"
	"sync"
	"testing"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/uber/h3-go/v4"
)

// Helper function to create a test entity
func createTestEntity(id string, lat, lng float64) entity.Entity {
	return entity.Entity{ID: id, Lat: lat, Lng: lng, Source: "test"}
}

const indexingResolution = 7

// TestNewIndex tests the NewIndex function
func TestNewIndex(t *testing.T) {
	idx := NewIndex()
	if idx == nil {
		t.Fatal("NewIndex returned nil")
	}
	if idx.entities == nil {
		t.Error("NewIndex did not initialize entities map")
	}
	if idx.layers == nil {
		t.Error("NewIndex did not initialize layers map")
	}
}

// TestUpdate_Add tests adding new entities
func TestUpdate_Add(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	e2 := createTestEntity("e2", 1.0, 1.0)
	idx.BatchUpdateRust([]entity.Entity{e1, e2}, nil)
	
	if len(idx.entities) != 2 {
		t.Errorf("Expected 2 entities, got %d", len(idx.entities))
	}
	internalID1 := idx.entityIDToInternalID[e1.ID]
	if _, ok := idx.entities[internalID1]; !ok {
		t.Errorf("Entity %s not found in entities map", e1.ID)
	}
	internalID2 := idx.entityIDToInternalID[e2.ID]
	if _, ok := idx.entities[internalID2]; !ok {
		t.Errorf("Entity %s not found in entities map", e2.ID)
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	h3e2, _ := h3.LatLngToCell(h3.LatLng{Lat: e2.Lat, Lng: e2.Lng}, indexingResolution)

	if len(idx.layers[indexingResolution][h3e1]) != 1 {
		t.Errorf("Expected 1 entity in h3e1 cell, got %d", len(idx.layers[indexingResolution][h3e1]))
	}
	
	foundE1 := false
	for _, internalID := range idx.layers[indexingResolution][h3e1] {
		if idx.idToEntityID[internalID] == e1.ID {
			foundE1 = true
			break
		}
	}
	if !foundE1 {
		t.Errorf("Entity %s not found in layers[7] for h3e1", e1.ID)
	}
	
	if len(idx.layers[indexingResolution][h3e2]) != 1 {
		t.Errorf("Expected 1 entity in h3e2 cell, got %d", len(idx.layers[indexingResolution][h3e2]))
	}
	foundE2 := false
	for _, internalID := range idx.layers[indexingResolution][h3e2] {
		if idx.idToEntityID[internalID] == e2.ID {
			foundE2 = true
			break
		}
	}
	if !foundE2 {
		t.Errorf("Entity %s not found in layers[7] for h3e2", e2.ID)
	}
}

// TestUpdate_Update tests updating existing entities, including cell changes
func TestUpdate_Update(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	idx.BatchUpdateRust([]entity.Entity{e1}, nil)

	// Update e1's position
	e1Updated := createTestEntity("e1", 0.01, 0.01)
	idx.BatchUpdateRust([]entity.Entity{e1Updated}, nil)
	
	if len(idx.entities) != 1 {
		t.Errorf("Expected 1 entity after update, got %d", len(idx.entities))
	}
	internalID := idx.entityIDToInternalID[e1.ID]
	if idx.entities[internalID].Lat != e1Updated.Lat || idx.entities[internalID].Lng != e1Updated.Lng {
		t.Errorf("Entity e1 not updated in entities map. Expected (%f,%f), got (%f,%f)", e1Updated.Lat, e1Updated.Lng, idx.entities[internalID].Lat, idx.entities[internalID].Lng)
	}

	oldH3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	newH3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1Updated.Lat, Lng: e1Updated.Lng}, indexingResolution)

	// If cell changed, old cell should be empty
	if oldH3e1 != newH3e1 {
		if ids, found := idx.layers[indexingResolution][oldH3e1]; found {
			if len(ids) > 0 {
				t.Errorf("Old H3 cell %d for e1 should be empty", oldH3e1)
			}
		}
	}

	if len(idx.layers[indexingResolution][newH3e1]) != 1 {
		t.Errorf("Expected 1 entity in new H3 cell %d, got %d", newH3e1, len(idx.layers[indexingResolution][newH3e1]))
	}
	foundUpdated := false
	for _, internalID := range idx.layers[indexingResolution][newH3e1] {
		if idx.idToEntityID[internalID] == e1Updated.ID {
			foundUpdated = true
			break
		}
	}
	if !foundUpdated {
		t.Errorf("Updated entity %s not found in layers[7] for new H3 cell", e1Updated.ID)
	}
}

// TestUpdate_Remove tests removing entities
func TestUpdate_Remove(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	e2 := createTestEntity("e2", 1.0, 1.0)
	idx.BatchUpdateRust([]entity.Entity{e1, e2}, nil)

	idx.BatchUpdateRust(nil, []string{e1.ID})
	
	if len(idx.entities) != 1 {
		t.Errorf("Expected 1 entity after removal, got %d", len(idx.entities))
	}
	internalID2 := idx.entityIDToInternalID[e2.ID]
	if _, ok := idx.entities[internalID2]; !ok {
		t.Errorf("Entity %s not found after removal, expected to remain", e2.ID)
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	if ids, found := idx.layers[indexingResolution][h3e1]; found {
		if len(ids) > 0 {
			t.Errorf("Old H3 cell %d for e1 should be empty after removal, but found %d entities", h3e1, len(ids))
		}
	}
	
	h3e2, _ := h3.LatLngToCell(h3.LatLng{Lat: e2.Lat, Lng: e2.Lng}, indexingResolution)
	if len(idx.layers[indexingResolution][h3e2]) != 1 {
		t.Errorf("Expected 1 entity in h3e2 cell after removal of e1, got %d", len(idx.layers[indexingResolution][h3e2]))
	}
}

// TestQuery_Visible tests querying for visible entities
func TestQuery_Visible(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 34.0522, -118.2437)
	e2 := createTestEntity("e2", 34.0522, -118.2437)
	e3 := createTestEntity("e3", 40.7128, -74.0060) // Outside viewport
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3}, nil)

	vpLA := entity.Viewport{North: 34.2, South: 33.9, East: -118.1, West: -118.4, Zoom: 7}
	visible := make([]entity.Entity, 0, 100)
	clusters := make(map[string]entity.Cluster)
	visible, err := idx.Query(vpLA, visible, clusters)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(clusters) != 1 {
		t.Errorf("Expected 1 cluster (for e3), got %d, clusters=%+v", len(clusters), clusters)
	}

	if len(visible) != 2 {
		t.Errorf("Expected 2 visible entities, got %d", len(visible))
	}
}

// TestQuery_Clusters tests querying for clusters
func TestQuery_Clusters(t *testing.T) {
	idx := NewIndex()
	// Create 4 entities clustered in the same cell
	e1 := createTestEntity("e1", 0.0, 0.0)
	e2 := createTestEntity("e2", 0.0001, 0.0001)
	e3 := createTestEntity("e3", 0.0002, 0.0002)
	e4 := createTestEntity("e4", 0.0003, 0.0003)
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3, e4}, nil)

	// Query at zoom 0 (resolution 3) -> Should be clustered
	vp := entity.Viewport{North: 1.0, South: -1.0, East: 1.0, West: -1.0, Zoom: 0}
	visible := make([]entity.Entity, 0, 100)
	clusters := make(map[string]entity.Cluster)
	_, _ = idx.Query(vp, visible, clusters)

	if len(clusters) == 0 {
		t.Errorf("Expected at least one cluster, got 0")
	}
	var totalClusteredEntities int
	for _, c := range clusters {
		totalClusteredEntities += c.Count
	}
	if totalClusteredEntities != 4 {
		t.Errorf("Expected 4 entities to be clustered, got %d", totalClusteredEntities)
	}
}

// TestQuery_Mixed tests querying for a mix of visible and clustered entities
func TestQuery_Mixed(t *testing.T) {
	idx := NewIndex()
	// Clustered
	e1 := createTestEntity("e1", 0.0, 0.0)
	e2 := createTestEntity("e2", 0.0001, 0.0001)
	// Visible
	e3 := createTestEntity("e3", 10.0, 10.0)
	e4 := createTestEntity("e4", 10.0001, 10.0001)
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3, e4}, nil)

	// Query with viewport covering only e3 and e4
	vp := entity.Viewport{North: 11.0, South: 9.0, East: 11.0, West: 9.0, Zoom: 7}
	visible := make([]entity.Entity, 0, 100)
	clusters := make(map[string]entity.Cluster)
	visible, _ = idx.Query(vp, visible, clusters)

	if len(visible) != 2 {
		t.Errorf("Expected 2 visible entities, got %d", len(visible))
	}
	if len(clusters) == 0 {
		t.Errorf("Expected 1 cluster (for e1,e2), got 0")
	}
	var totalClusteredEntities int
	for _, c := range clusters {
		totalClusteredEntities += c.Count
	}
	if totalClusteredEntities != 2 {
		t.Errorf("Expected 2 entities to be clustered, got %d", totalClusteredEntities)
	}
}

// TestQuery_EmptyIndex tests querying an empty index
func TestQuery_EmptyIndex(t *testing.T) {
	idx := NewIndex()
	vp := entity.Viewport{
		North: 90.0, South: -90.0, East: 180.0, West: -180.0, Zoom: 7,
	}

	visible := make([]entity.Entity, 0, 100)
	clusters := make(map[string]entity.Cluster)
	visible, err := idx.Query(vp, visible, clusters)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(visible) != 0 {
		t.Errorf("Expected 0 visible entities, got %d", len(visible))
	}
	if len(clusters) != 0 {
		t.Errorf("Expected 0 clusters, got %d", len(clusters))
	}
}

// TestUpdate_NoCellChange tests updating an entity without changing its H3 cell
func TestUpdate_NoCellChange(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	idx.BatchUpdateRust([]entity.Entity{e1}, nil)

	e1Updated := createTestEntity("e1", 0.0001, 0.0001)
	e1Updated.Source = "updated_test"
	idx.BatchUpdateRust([]entity.Entity{e1Updated}, nil)

	internalID := idx.entityIDToInternalID[e1.ID]
	if idx.entities[internalID].Source != "updated_test" {
		t.Errorf("Entity e1 source not updated")
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	if len(idx.layers[indexingResolution][h3e1]) != 1 {
		t.Errorf("Expected 1 entity in H3 cell %d, got %d", h3e1, len(idx.layers[indexingResolution][h3e1]))
	}
	found := false
	for _, internalID := range idx.layers[indexingResolution][h3e1] {
		if idx.idToEntityID[internalID] == e1Updated.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Entity %s not found in layers[7] for h3e1", e1Updated.ID)
	}
}

// TestQuery_BoundaryMovement tests if entities are correctly updated/moved between viewport boundaries
func TestQuery_BoundaryMovement(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	idx.BatchUpdateRust([]entity.Entity{e1}, nil)

	// Viewport covering the entity
	vp := entity.Viewport{North: 1.0, South: -1.0, East: 1.0, West: -1.0, Zoom: 7}
	
	// Query 1: Entity visible
	visible := make([]entity.Entity, 0, 100)
	clusters := make(map[string]entity.Cluster)
	visible, _ = idx.Query(vp, visible, clusters)
	if len(visible) != 1 {
		t.Errorf("Expected 1 visible entity, got %d", len(visible))
	}

	// Move entity out of viewport
	e1Updated := createTestEntity("e1", 10.0, 10.0)
	idx.BatchUpdateRust([]entity.Entity{e1Updated}, nil)

	// Query 2: Entity not visible (should be in clusters if outside)
	visible = make([]entity.Entity, 0, 100)
	clusters = make(map[string]entity.Cluster)
	visible, _ = idx.Query(vp, visible, clusters)
	if len(visible) != 0 {
		t.Errorf("Expected 0 visible entities, got %d", len(visible))
	}
	if len(clusters) == 0 {
		t.Errorf("Expected at least one cluster for entity outside viewport, got 0")
	}
}

// TestQuery_ZoomTransition tests if entities correctly transition from visible to clustered when zooming out
func TestQuery_ZoomTransition(t *testing.T) {
	idx := NewIndex()
	e1 := createTestEntity("e1", 0.0, 0.0)
	idx.BatchUpdateRust([]entity.Entity{e1}, nil)

	// Zoomed in (Resolution 7) -> Visible
	vpIn := entity.Viewport{North: 1.0, South: -1.0, East: 1.0, West: -1.0, Zoom: 7}
	visibleIn := make([]entity.Entity, 0, 100)
	clustersIn := make(map[string]entity.Cluster)
	visibleIn, _ = idx.Query(vpIn, visibleIn, clustersIn)
	if len(visibleIn) != 1 {
		t.Errorf("Expected 1 visible entity, got %d", len(visibleIn))
	}

	// Zoomed out (Resolution 3) -> Clustered
	vpOut := entity.Viewport{North: 90.0, South: -90.0, East: 180.0, West: -180.0, Zoom: 0}
	visibleOut := make([]entity.Entity, 0, 100)
	clustersOut := make(map[string]entity.Cluster)
	visibleOut, _ = idx.Query(vpOut, visibleOut, clustersOut)
	if len(visibleOut) != 0 {
		t.Errorf("Expected 0 visible entities, got %d", len(visibleOut))
	}
	if len(clustersOut) == 0 {
		t.Errorf("Expected at least one cluster, got 0")
	}
}

func TestIndex_ConcurrentAccessStress(t *testing.T) {
	idx := NewIndex()
	const (
		numRoutines   = 2
		opsPerRoutine = 10
	)
	var wg sync.WaitGroup
	wg.Add(numRoutines * 2)

	// Writers
	for i := 0; i < numRoutines; i++ {
		go func(rID int) {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				entityID := fmt.Sprintf("e-%d-%d", rID, j)
				e := entity.Entity{
					ID:  entityID,
					Lat: float64(j),
					Lng: float64(j),
				}
				idx.BatchUpdateRust([]entity.Entity{e}, nil)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numRoutines; i++ {
		go func(rID int) {
			defer wg.Done()
			for j := 0; j < opsPerRoutine; j++ {
				vp := entity.Viewport{
					North: 90, South: -90, East: 180, West: -180, Zoom: 7,
				}
				visible := make([]entity.Entity, 0, 100)
				clusters := make(map[string]entity.Cluster)
				_, _ = idx.Query(vp, visible, clusters)
			}
		}(i)
	}
	wg.Wait()
}

// TestIndex_CGO_BoundaryInvariants tests edge cases for the CGO boundary.
func TestIndex_CGO_BoundaryInvariants(t *testing.T) {
	tests := []struct {
		name     string
		entities []entity.Entity
		removed  []string
	}{
		{"Case A: Empty", nil, nil},
		{"Case B: Invalid Coords", []entity.Entity{{ID: "invalid", Lat: 195.0, Lng: -360.0}}, nil},
		{"Case C: Massive Batch", make([]entity.Entity, 5000), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := NewIndex()
			// Generate massive batch data
			if tt.name == "Case C: Massive Batch" {
				for i := 0; i < len(tt.entities); i++ {
					tt.entities[i] = entity.Entity{
						ID:  fmt.Sprintf("e-%d", i),
						Lat: 0.0,
						Lng: 0.0,
					}
				}
			}

			// Perform update - should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("BatchUpdateRust panicked: %v", r)
				}
			}()
			idx.BatchUpdateRust(tt.entities, tt.removed)

			// Assertions
			if tt.name == "Case B: Invalid Coords" {
				// We expect the entity to be in idx.entities but not necessarily indexed if the Rust side returns 0.
				// If the Rust side returned a valid cell, it is indexed.
				// The key is that the system should not panic.
			}
			if tt.name == "Case C: Massive Batch" {
				// Check that all 5000 entities were indexed
				if len(idx.entities) != 5000 {
					t.Errorf("Expected 5000 entities, got %d", len(idx.entities))
				}
			}
		})
	}
}
