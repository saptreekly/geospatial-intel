package spatial

import (
	"sync"

	"github.com/jackweekly/OSINT/entity"
	"github.com/uber/h3-go/v4"
)

// Pre-computed lookup per required zoom resolution layer
var targetResolutions = []int{2, 4, 6, 7}

// Index is a thread-safe spatial index using H3.
type Index struct {
	mu        sync.RWMutex
	entities  map[string]entity.Entity
	layers    map[int]map[h3.Cell]map[string]entity.Entity
}

// NewIndex creates a new spatial index.
func NewIndex() *Index {
	layers := make(map[int]map[h3.Cell]map[string]entity.Entity)
	for _, res := range targetResolutions {
		layers[res] = make(map[h3.Cell]map[string]entity.Entity)
	}
	return &Index{
		entities: make(map[string]entity.Entity),
		layers:   layers,
	}
}

// Update adds or updates entities and removes stale ones.
func (idx *Index) Update(entities []entity.Entity, removed []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// 1. Remove entities
	for _, id := range removed {
		if oldEntity, ok := idx.entities[id]; ok {
			// Remove from all layers
			latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				cell, err := h3.LatLngToCell(latLng, res)
				if err == nil {
					if cellMap, found := idx.layers[res][cell]; found {
						delete(cellMap, id)
						if len(cellMap) == 0 {
							delete(idx.layers[res], cell)
						}
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
		latLng := h3.LatLng{Lat: e.Lat, Lng: e.Lng}

		if exists {
			// If entity existed, remove from all layers
			oldLatLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				oldCell, oldErr := h3.LatLngToCell(oldLatLng, res)
				if oldErr == nil {
					if cellMap, found := idx.layers[res][oldCell]; found {
						delete(cellMap, e.ID)
						if len(cellMap) == 0 {
							delete(idx.layers[res], oldCell)
						}
					}
				}
			}
		}

		// Add/update in entities map
		idx.entities[e.ID] = e

		// Add/update in all layers
		for _, res := range targetResolutions {
			newCell, err := h3.LatLngToCell(latLng, res)
			if err != nil {
				continue
			}
			if _, ok := idx.layers[res][newCell]; !ok {
				idx.layers[res][newCell] = make(map[string]entity.Entity)
			}
			idx.layers[res][newCell][e.ID] = e
		}
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

	// Loop over the client's viewport cells - this is now O(viewport cells)
	layer := idx.layers[queryResolution]
	for _, viewCell := range viewportCells {
		if entitiesInCell, found := layer[viewCell]; found {
			for _, e := range entitiesInCell {
				if _, alreadyProcessed := processedEntities[e.ID]; alreadyProcessed {
					continue
				}

				if !onlyClusters {
					visible = append(visible, e)
				} else {
					clusterCounts[viewCell]++
				}
				processedEntities[e.ID] = struct{}{}
			}
		}
	}

	// ALSO iterate over ALL indexed cells to find entities NOT in viewport to cluster them
	for cell, entitiesInCell := range layer {
		inViewport := false
		for _, vc := range viewportCells {
			if vc == cell {
				inViewport = true
				break
			}
		}

		if !inViewport {
			for _, e := range entitiesInCell {
				if _, alreadyProcessed := processedEntities[e.ID]; alreadyProcessed {
					continue
				}
				clusterCounts[cell]++
				processedEntities[e.ID] = struct{}{}
			}
		}
	}

	// Convert cluster cells to entity.Cluster with centroids
	clusters = make(map[string]entity.Cluster)
	for cell, count := range clusterCounts {
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
