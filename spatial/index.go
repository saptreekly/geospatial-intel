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

	viewportCellSet := make(map[h3.Cell]struct{})
	for _, cell := range viewportCells {
		viewportCellSet[cell] = struct{}{}
	}

	visible = make([]entity.Entity, 0)
	clusterCounts := make(map[h3.Cell]int)
	onlyClusters := vp.Zoom < 6

	// Iterate through the cellIndex
	for indexedCell, entitiesInCell := range idx.cellIndex {
		// Get the parent H3 cell at the query resolution for the indexed cell
		entityQueryCell, _ := indexedCell.Parent(queryResolution)

		_, inViewport := viewportCellSet[entityQueryCell]

		for _, e := range entitiesInCell { // Iterate entities within this indexedCell
			if inViewport && !onlyClusters {
				visible = append(visible, e)
			} else {
				// Determine cluster resolution
				clusterResolution := queryResolution
				if !inViewport {
					// If outside viewport, cluster at a coarser resolution
					clusterResolution = queryResolution - 1
					if clusterResolution < 0 {
						clusterResolution = 0
					}
				}
				
				// Calculate the H3 cell for clustering based on the entity's Lat/Lng
				// This ensures consistent clustering regardless of indexingResolution
				clusterCell, err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, clusterResolution)
				if err == nil { 
					clusterCounts[clusterCell]++
				}
			}
		}
	}
	
	// Convert cluster cells to entity.Cluster with centroids
	clusters = make(map[string]entity.Cluster)
	for cell, count := range clusterCounts {
		// Only send clusters with > 1 entity OR if they are outside the viewport
		// If it's a single entity inside the viewport, it should have been in 'visible' 
		// unless onlyClusters is true.
		if count > 1 || onlyClusters {
			latLng, _ := h3.CellToLatLng(cell)
			clusters[cell.String()] = entity.Cluster{
				Lat:   latLng.Lat,
				Lng:   latLng.Lng,
				Count: count,
			}
		}
	}

	return visible, clusters, nil
}
