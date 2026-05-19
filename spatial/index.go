package spatial

import (
	"sync"

	"github.com/jackweekly/geospatial-server/entity"
	"github.com/uber/h3-go/v4"
)

// Index is a thread-safe spatial index using H3.
type Index struct {
	mu       sync.RWMutex
	entities map[string]entity.Entity // entity ID → entity
}

// NewIndex creates a new spatial index.
func NewIndex() *Index {
	return &Index{
		entities: make(map[string]entity.Entity),
	}
}

// Update adds or updates entities and removes stale ones.
func (idx *Index) Update(entities []entity.Entity, removed []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Add or update entities
	for _, e := range entities {
		idx.entities[e.ID] = e
	}

	// Remove entities
	for _, id := range removed {
		delete(idx.entities, id)
	}
}

// Query returns entities visible in the viewport and cluster counts for surrounding areas.
func (idx *Index) Query(vp entity.Viewport) (visible []entity.Entity, clusters map[string]entity.Cluster, err error) {
	resolution := ZoomToResolution(vp.Zoom)
	viewportCells, err := ViewportToCells(vp, resolution)
	if err != nil {
		return nil, nil, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Build a set of viewport cells for fast lookup
	viewportCellSet := make(map[h3.Cell]struct{})
	for _, cell := range viewportCells {
		viewportCellSet[cell] = struct{}{}
	}

	visible = make([]entity.Entity, 0)
	clusterCounts := make(map[h3.Cell]int)

	// Determine if we should only show clusters based on zoom
	// If zoom < 6, we mostly want clusters even inside the viewport
	onlyClusters := vp.Zoom < 6

	for _, e := range idx.entities {
		entityCell, err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, resolution)
		if err != nil {
			continue
		}

		_, inViewport := viewportCellSet[entityCell]

		if inViewport && !onlyClusters {
			visible = append(visible, e)
		} else {
			// Count as cluster
			// For clustering, use a lower resolution if zoomed out
			clusterResolution := resolution
			if !inViewport {
				clusterResolution = resolution - 1
				if clusterResolution < 0 {
					clusterResolution = 0
				}
			}
			
			clusterCell, err := h3.LatLngToCell(h3.LatLng{Lat: e.Lat, Lng: e.Lng}, clusterResolution)
			if err == nil {
				// Only include clusters that are "near" the viewport if not inViewport
				// For simplicity, we'll just cluster everything for now but 
				// we could filter by a expanded viewport.
				clusterCounts[clusterCell]++
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
