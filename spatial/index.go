package spatial

import (
	"sync"

	"github.com/jackweekly/OSINT/entity"
	"github.com/uber/h3-go/v4"
)

const indexingResolution = 7 // A reasonable default H3 resolution for indexing entities

// Index is a thread-safe spatial index using H3.
type Index struct {
	mu        sync.RWMutex
	entities  map[string]entity.Entity              // entity ID → entity
	cellIndex map[h3.Cell]map[string]entity.Entity // h3 cell → (entity ID → entity)
}

// NewIndex creates a new spatial index.
func NewIndex() *Index {
	return &Index{
		entities:  make(map[string]entity.Entity),
		cellIndex: make(map[h3.Cell]map[string]entity.Entity),
	}
}

// Update adds or updates entities and removes stale ones.
func (idx *Index) Update(entities []entity.Entity, removed []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// 1. Remove entities
	for _, id := range removed {
		if oldEntity, ok := idx.entities[id]; ok {
			// Remove from cellIndex
			oldH3Cell, err := h3.LatLngToCell(h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}, indexingResolution)
			if err == nil {
				if cellMap, found := idx.cellIndex[oldH3Cell]; found {
					delete(cellMap, id)
					if len(cellMap) == 0 {
						delete(idx.cellIndex, oldH3Cell)
					}
				}
			}
			// Remove from entities map
			delete(idx.entities, id)
		}
	}

	// 2. Add or update entities
	for _, e := range entities {
		oldEntity, exists := idx.entities[e.ID]
		newH3Cell, err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, indexingResolution)
		if err != nil {
			// Log error or handle gracefully if entity cannot be mapped to H3 cell
			continue
		}

		if exists {
			// If entity existed, check if its H3 cell changed
			oldH3Cell, oldErr := h3.LatLngToCell(h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}, indexingResolution)
			if oldErr == nil && oldH3Cell != newH3Cell {
				// Remove from old cell in cellIndex
				if cellMap, found := idx.cellIndex[oldH3Cell]; found {
					delete(cellMap, e.ID)
					if len(cellMap) == 0 {
						delete(idx.cellIndex, oldH3Cell)
					}
				}
			}
		}

		// Add/update in entities map
		idx.entities[e.ID] = e

		// Add/update in cellIndex
		if _, ok := idx.cellIndex[newH3Cell]; !ok {
			idx.cellIndex[newH3Cell] = make(map[string]entity.Entity)
		}
		idx.cellIndex[newH3Cell][e.ID] = e
	}
}


// Query returns entities visible in the viewport and cluster counts for surrounding areas.
func (idx *Index) Query(vp entity.Viewport) (visible []entity.Entity, clusters map[string]entity.Cluster, err error) {
	queryResolution := ZoomToResolution(vp.Zoom)
	viewportCells, err := ViewportToCells(vp, queryResolution)
	if err != nil {
		return nil, nil, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	visible = make([]entity.Entity, 0)
	clusterCounts := make(map[h3.Cell]int)
	onlyClusters := vp.Zoom < 6 // True if zoomed out, showing only clusters

	// Keep track of entities already added to visible or clusterCounts
	processedEntities := make(map[string]struct{})

	// Loop over the client's viewport cells instead of the global map
	for _, viewCell := range viewportCells {
		var cellsToIndex []h3.Cell
		if queryResolution == indexingResolution {
			cellsToIndex = []h3.Cell{viewCell}
		} else { // queryResolution < indexingResolution (zoomed out)
			children, h3Err := viewCell.Children(indexingResolution)
			if h3Err != nil {
				// Handle error, e.g., continue to next viewCell
				continue
			}
			cellsToIndex = children
		}

		for _, cell := range cellsToIndex {
			if entitiesInCell, found := idx.cellIndex[cell]; found {
				for _, e := range entitiesInCell {
					if _, alreadyProcessed := processedEntities[e.ID]; alreadyProcessed {
						continue // Avoid processing same entity multiple times if children overlap
					}

					// Re-calculate the cell of entity at query resolution to check viewport membership
					entityQueryCell, h3Err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, queryResolution)
					if h3Err != nil {
						continue
					}

					// Verify if entityQueryCell matches one of the viewportCells
					inViewport := false
					for _, vc := range viewportCells {
						if vc == entityQueryCell {
							inViewport = true
							break
						}
					}

					if inViewport && !onlyClusters {
						visible = append(visible, e)
					}
					
					// ALWAYS cluster if onlyClusters is true OR if entity is NOT in viewport
					if onlyClusters || !inViewport {
						if entityQueryCell != 0 {
							clusterCounts[entityQueryCell]++
						}
					}
					processedEntities[e.ID] = struct{}{} // Mark as processed
				}
			}
		}
	}

	// ALSO iterate over ALL indexed cells to find entities NOT in viewport to cluster them
	for indexedCell, entitiesInCell := range idx.cellIndex {
		entityQueryCell, _ := indexedCell.Parent(queryResolution)
		
		inViewport := false
		for _, vc := range viewportCells {
			if vc == entityQueryCell {
				inViewport = true
				break
			}
		}
		
		if !inViewport {
			for _, e := range entitiesInCell {
				if _, alreadyProcessed := processedEntities[e.ID]; alreadyProcessed {
					continue
				}
				clusterCell, h3Err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, queryResolution)
				if h3Err == nil && clusterCell != 0 {
					clusterCounts[clusterCell]++
				}
				processedEntities[e.ID] = struct{}{}
			}
		}
	}

	// Convert cluster cells to entity.Cluster with centroids
	clusters = make(map[string]entity.Cluster)
	for cell, count := range clusterCounts {
		// Determine if this cell is within the viewport
		inViewport := false
		for _, vc := range viewportCells {
			if vc == cell {
				inViewport = true
				break
			}
		}

		// Logic:
		// - If in viewport: only cluster if count > 1 OR zoomed out (onlyClusters)
		// - If outside viewport: cluster if count > 0
		if inViewport {
			if count > 1 || onlyClusters {
				latLng, _ := h3.CellToLatLng(cell)
				clusters[cell.String()] = entity.Cluster{
					Lat:   latLng.Lat,
					Lng:   latLng.Lng,
					Count: count,
				}
			}
		} else {
			if count > 0 {
				latLng, _ := h3.CellToLatLng(cell)
				clusters[cell.String()] = entity.Cluster{
					Lat:   latLng.Lat,
					Lng:   latLng.Lng,
					Count: count,
				}
			}
		}
	}


	return visible, clusters, nil
}
