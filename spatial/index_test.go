package spatial

import (
	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/uber/h3-go/v4"
	"testing"
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
	if _, ok := idx.entities[e1.ID]; !ok {
		t.Errorf("Entity %s not found in entities map", e1.ID)
	}
	if _, ok := idx.entities[e2.ID]; !ok {
		t.Errorf("Entity %s not found in entities map", e2.ID)
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	h3e2, _ := h3.LatLngToCell(h3.LatLng{Lat: e2.Lat, Lng: e2.Lng}, indexingResolution)

	if len(idx.layers[indexingResolution][h3e1]) != 1 {
		t.Errorf("Expected 1 entity in h3e1 cell, got %d", len(idx.layers[indexingResolution][h3e1]))
	}
	if _, ok := idx.layers[indexingResolution][h3e1][e1.ID]; !ok {
		t.Errorf("Entity %s not found in layers[7] for h3e1", e1.ID)
	}
	if len(idx.layers[indexingResolution][h3e2]) != 1 {
		t.Errorf("Expected 1 entity in h3e2 cell, got %d", len(idx.layers[indexingResolution][h3e2]))
	}
	if _, ok := idx.layers[indexingResolution][h3e2][e2.ID]; !ok {
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
	if idx.entities[e1.ID].Lat != e1Updated.Lat || idx.entities[e1.ID].Lng != e1Updated.Lng {
		t.Errorf("Entity e1 not updated in entities map. Expected (%f,%f), got (%f,%f)", e1Updated.Lat, e1Updated.Lng, idx.entities[e1.ID].Lat, idx.entities[e1.ID].Lng)
	}

	oldH3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	newH3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1Updated.Lat, Lng: e1Updated.Lng}, indexingResolution)

	// If cell changed, old cell should be empty
	if oldH3e1 != newH3e1 {
		if _, found := idx.layers[indexingResolution][oldH3e1]; found {
			t.Errorf("Old H3 cell %d for e1 should be empty", oldH3e1)
		}
	}

	if len(idx.layers[indexingResolution][newH3e1]) != 1 {
		t.Errorf("Expected 1 entity in new H3 cell %d, got %d", newH3e1, len(idx.layers[indexingResolution][newH3e1]))
	}
	if _, ok := idx.layers[indexingResolution][newH3e1][e1Updated.ID]; !ok {
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
	if _, ok := idx.entities[e1.ID]; ok {
		t.Errorf("Entity %s should have been removed from entities map", e1.ID)
	}
	if _, ok := idx.entities[e2.ID]; !ok {
		t.Errorf("Entity %s not found after removal, expected to remain", e2.ID)
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	if _, found := idx.layers[indexingResolution][h3e1]; found {
		t.Errorf("Old H3 cell %d for e1 should be empty after removal", h3e1)
	}
	h3e2, _ := h3.LatLngToCell(h3.LatLng{Lat: e2.Lat, Lng: e2.Lng}, indexingResolution)
	if len(idx.layers[indexingResolution][h3e2]) != 1 {
		t.Errorf("Expected 1 entity in h3e2 cell after removal of e1, got %d", len(idx.layers[indexingResolution][h3e2]))
	}
}

// TestQuery_Visible tests querying for visible entities
func TestQuery_Visible(t *testing.T) {
	idx := NewIndex()
	// Entities in a known cell at indexingResolution=7
	e1 := createTestEntity("e1", 34.0522, -118.2437) // Los Angeles
	e2 := createTestEntity("e2", 34.0522, -118.2437) // Same cell as e1
	e3 := createTestEntity("e3", 40.7128, -74.0060)  // New York
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3}, nil)

	// Viewport covering Los Angeles at zoom level 7 (resolution 7)
	// (North, South, East, West)
	vpLA := entity.Viewport{
		North: 34.2, South: 33.9, East: -118.1, West: -118.4, Zoom: 7, // Made viewport larger
	}

	visible, clusters, err := idx.Query(vpLA)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(clusters) != 1 {
		t.Errorf("Expected 1 cluster (for e3), got %d, clusters=%+v", len(clusters), clusters)
	}

	if len(visible) != 2 {
		t.Errorf("Expected 2 visible entities, got %d", len(visible))
	}

	foundE1, foundE2 := false, false
	for _, e := range visible {
		if e.ID == e1.ID {
			foundE1 = true
		}
		if e.ID == e2.ID {
			foundE2 = true
		}
	}
	if !foundE1 || !foundE2 {
		t.Errorf("Did not find expected visible entities e1 and e2")
	}
}

// TestQuery_Clusters tests querying for clusters
func TestQuery_Clusters(t *testing.T) {
	idx := NewIndex()
	// Entities far apart
	e1 := createTestEntity("e1", 0.0, 0.0)
	e2 := createTestEntity("e2", 0.1, 0.1)
	e3 := createTestEntity("e3", 5.0, 5.0)
	e4 := createTestEntity("e4", 5.1, 5.1)
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3, e4}, nil)

	// Very zoomed out viewport (zoom 0 -> resolution 2), everything should cluster
	vpGlobal := entity.Viewport{
		North: 90.0, South: -90.0, East: 180.0, West: -180.0, Zoom: 0,
	}

	visible, clusters, err := idx.Query(vpGlobal)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(visible) != 0 {
		t.Errorf("Expected no visible entities, got %d", len(visible))
	}
	if len(clusters) < 1 { // At least one cluster expected
		t.Errorf("Expected at least one cluster, got %d", len(clusters))
	}

	// Verify cluster counts (e1, e2 likely in one cluster, e3, e4 in another)
	// Due to varying resolutions and h3 behavior, verifying exact cluster counts might be complex
	// for arbitrary lat/lng. Focus on presence of clusters.
	totalClusteredEntities := 0
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
	// e1, e2 in viewport, e3, e4 outside
	e1 := createTestEntity("e1", 34.0522, -118.2437) // LA
	e2 := createTestEntity("e2", 34.0550, -118.2500) // LA nearby
	e3 := createTestEntity("e3", 40.7128, -74.0060)  // NY
	e4 := createTestEntity("e4", 40.7130, -74.0070)  // NY nearby
	idx.BatchUpdateRust([]entity.Entity{e1, e2, e3, e4}, nil)

	// Viewport covering Los Angeles at zoom 7 (resolution 7)
	vpLA := entity.Viewport{
		North: 34.2, South: 33.9, East: -118.1, West: -118.4, Zoom: 7, // Made viewport larger
	}

	visible, clusters, err := idx.Query(vpLA)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(visible) != 2 {
		t.Errorf("Expected 2 visible entities, got %d", len(visible))
	}
	if len(clusters) != 1 { // Expect e3, e4 to form a single cluster outside LA viewport
		t.Errorf("Expected 1 cluster, got %d", len(clusters))
	}

	foundE1, foundE2 := false, false
	for _, e := range visible {
		if e.ID == e1.ID {
			foundE1 = true
		}
		if e.ID == e2.ID {
			foundE2 = true
		}
	}
	if !foundE1 || !foundE2 {
		t.Errorf("Did not find expected visible entities e1 and e2")
	}

	// Verify the cluster for e3, e4
	totalClusteredEntities := 0
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

	visible, clusters, err := idx.Query(vp)
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

	// Update e1's non-spatial attribute
	e1Updated := e1
	e1Updated.Source = "updated_test"
	idx.BatchUpdateRust([]entity.Entity{e1Updated}, nil)

	if len(idx.entities) != 1 {
		t.Errorf("Expected 1 entity after update, got %d", len(idx.entities))
	}
	if idx.entities[e1.ID].Source != "updated_test" {
		t.Errorf("Entity e1 source not updated")
	}

	h3e1, _ := h3.LatLngToCell(h3.LatLng{Lat: e1.Lat, Lng: e1.Lng}, indexingResolution)
	if len(idx.layers[indexingResolution][h3e1]) != 1 {
		t.Errorf("Expected 1 entity in H3 cell %d, got %d", h3e1, len(idx.layers[indexingResolution][h3e1]))
	}
	if idx.layers[indexingResolution][h3e1][e1Updated.ID].Source != "updated_test" {
		t.Errorf("Entity source in layers[7] not updated")
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
	visible, _, _ := idx.Query(vp)
	if len(visible) != 1 {
		t.Errorf("Expected 1 visible entity, got %d", len(visible))
	}

	// Move entity out of viewport
	e1Updated := createTestEntity("e1", 10.0, 10.0)
	idx.BatchUpdateRust([]entity.Entity{e1Updated}, nil)

	// Query 2: Entity not visible (should be in clusters if outside)
	visible, clusters, _ := idx.Query(vp)
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
	visibleIn, _, _ := idx.Query(vpIn)
	if len(visibleIn) != 1 {
		t.Errorf("Expected 1 visible entity, got %d", len(visibleIn))
	}

	// Zoomed out (Resolution 2) -> Clustered
	vpOut := entity.Viewport{North: 90.0, South: -90.0, East: 180.0, West: -180.0, Zoom: 0}
	visibleOut, clustersOut, _ := idx.Query(vpOut)
	if len(visibleOut) != 0 {
		t.Errorf("Expected 0 visible entities, got %d", len(visibleOut))
	}
	if len(clustersOut) == 0 {
		t.Errorf("Expected at least one cluster, got 0")
	}
}
