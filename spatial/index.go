package spatial

/*
#cgo LDFLAGS: -L../spatial_engine/target/release -lspatial_engine
#include <stdint.h>
#include <stddef.h>

int compute_resolutions_batch(
    const double* lats,
    const double* lngs,
    size_t count,
    uint64_t* out_res2,
    uint64_t* out_res4,
    uint64_t* out_res6,
    uint64_t* out_res7
);
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/util"
	"github.com/uber/h3-go/v4"
)

// Pre-computed lookup per required zoom resolution layer
var targetResolutions = []int{2, 4, 6, 7}

// Index is a thread-safe spatial index using H3.
type Index struct {
	mu               sync.RWMutex
	entities         map[string]entity.Entity
	layers           map[int]map[h3.Cell][]string
	globalCellCounts map[int]map[h3.Cell]int
	totalGlobalCounts map[int]int
	// Reusable vector scratchpads
	latsBuf, lngsBuf                   []float64
	res2Buf, res4Buf, res6Buf, res7Buf []uint64
}

// NewIndex creates a new spatial index.
func NewIndex() *Index {
	layers := make(map[int]map[h3.Cell][]string)
	globalCellCounts := make(map[int]map[h3.Cell]int)
	totalGlobalCounts := make(map[int]int)
	for _, res := range targetResolutions {
		layers[res] = make(map[h3.Cell][]string)
		globalCellCounts[res] = make(map[h3.Cell]int)
		totalGlobalCounts[res] = 0
	}
	initialCap := 100000
	return &Index{
		entities:          make(map[string]entity.Entity),
		layers:            layers,
		globalCellCounts:  globalCellCounts,
		totalGlobalCounts: totalGlobalCounts,
		latsBuf:           make([]float64, 0, initialCap),
		lngsBuf:           make([]float64, 0, initialCap),
		res2Buf:           make([]uint64, 0, initialCap),
		res4Buf:           make([]uint64, 0, initialCap),
		res6Buf:           make([]uint64, 0, initialCap),
		res7Buf:           make([]uint64, 0, initialCap),
	}
}

// getH3Cells computes H3 indices for all target resolutions for a given location.
func (idx *Index) getH3Cells(lat, lng float64) map[int]h3.Cell {
	cells := make(map[int]h3.Cell)
	latLng := h3.LatLng{Lat: lat, Lng: lng}
	for _, res := range targetResolutions {
		cell, err := h3.LatLngToCell(latLng, res)
		if err == nil {
			cells[res] = cell
		}
	}
	return cells
}

// BatchUpdateRust updates entities using the Rust spatial engine.
func (idx *Index) BatchUpdateRust(entities []entity.Entity, removed []string) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "BatchUpdateRust")

	if len(entities) == 0 && len(removed) == 0 {
		return
	}

	// Prepare data for CGO call
	count := len(entities)

	// Ensure buffers are large enough
	if count > cap(idx.latsBuf) {
		idx.latsBuf = make([]float64, count)
		idx.lngsBuf = make([]float64, count)
		idx.res2Buf = make([]uint64, count)
		idx.res4Buf = make([]uint64, count)
		idx.res6Buf = make([]uint64, count)
		idx.res7Buf = make([]uint64, count)
	} else {
		idx.latsBuf = idx.latsBuf[:count]
		idx.lngsBuf = idx.lngsBuf[:count]
		idx.res2Buf = idx.res2Buf[:count]
		idx.res4Buf = idx.res4Buf[:count]
		idx.res6Buf = idx.res6Buf[:count]
		idx.res7Buf = idx.res7Buf[:count]
	}

	for i, e := range entities {
		idx.latsBuf[i] = e.Lat
		idx.lngsBuf[i] = e.Lng
	}

	// Call Rust engine
	if count > 0 {
		C.compute_resolutions_batch(
			(*C.double)(unsafe.Pointer(&idx.latsBuf[0])),
			(*C.double)(unsafe.Pointer(&idx.lngsBuf[0])),
			C.size_t(count),
			(*C.uint64_t)(unsafe.Pointer(&idx.res2Buf[0])),
			(*C.uint64_t)(unsafe.Pointer(&idx.res4Buf[0])),
			(*C.uint64_t)(unsafe.Pointer(&idx.res6Buf[0])),
			(*C.uint64_t)(unsafe.Pointer(&idx.res7Buf[0])),
		)
	}

	// Update internal state
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.removeEntitiesLocked(removed)
	// Pass buffered slices instead of full length to insertEntitiesLocked
	idx.insertEntitiesLocked(entities, idx.res2Buf, idx.res4Buf, idx.res6Buf, idx.res7Buf)
}

func (idx *Index) removeEntitiesLocked(removed []string) {
	for _, id := range removed {
		if oldEntity, ok := idx.entities[id]; ok {
			latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				cell, err := h3.LatLngToCell(latLng, res)
				if err == nil {
					idx.globalCellCounts[res][cell]--
					if idx.globalCellCounts[res][cell] == 0 {
						delete(idx.globalCellCounts[res], cell)
					}
					idx.totalGlobalCounts[res]-- // Decrement total count

					if ids, found := idx.layers[res][cell]; found {
						for i, existingID := range ids {
							if existingID == id {
								idx.layers[res][cell] = append(ids[:i], ids[i+1:]...)
								break
							}
						}
						if len(idx.layers[res][cell]) == 0 {
							delete(idx.layers[res], cell)
						}
					}
				}
			}
			delete(idx.entities, id)
		}
	}
}

func (idx *Index) insertEntitiesLocked(entities []entity.Entity, res2, res4, res6, res7 []uint64) {
	for i, e := range entities {
		oldEntity, exists := idx.entities[e.ID]
		if exists {
			latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				oldCell, oldErr := h3.LatLngToCell(latLng, res)
				if oldErr == nil {
					idx.globalCellCounts[res][oldCell]--
					if idx.globalCellCounts[res][oldCell] == 0 {
						delete(idx.globalCellCounts[res], oldCell)
					}
					idx.totalGlobalCounts[res]-- // Decrement total count

					if ids, found := idx.layers[res][oldCell]; found {
						for j, existingID := range ids {
							if existingID == e.ID {
								idx.layers[res][oldCell] = append(ids[:j], ids[j+1:]...)
								break
							}
						}
						if len(idx.layers[res][oldCell]) == 0 {
							delete(idx.layers[res], oldCell)
						}
					}
				}
			}
		}

		idx.entities[e.ID] = e

		// Use H3 indices from Rust
		newCells := [4]h3.Cell{h3.Cell(res2[i]), h3.Cell(res4[i]), h3.Cell(res6[i]), h3.Cell(res7[i])}
		for j, res := range targetResolutions {
			cell := newCells[j]
			if cell == 0 {
				continue
			}
			idx.globalCellCounts[res][cell]++
			idx.totalGlobalCounts[res]++ // Increment total count

			idx.layers[res][cell] = append(idx.layers[res][cell], e.ID)
		}
	}
}

// Query returns entities visible in the viewport and cluster counts for surrounding areas.
func (idx *Index) Query(vp entity.Viewport) (visible []entity.Entity, clusters map[string]entity.Cluster, err error) {
	start := time.Now()
	defer util.LogIfSlow(start, 10*time.Millisecond, "Query")

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

	// 1. Loop over the client's viewport cells - this is now O(viewport cells)
	viewportCellSet := make(map[h3.Cell]struct{})
	for _, vc := range viewportCells {
		viewportCellSet[vc] = struct{}{}
	}

	layer := idx.layers[queryResolution]
	totalEntitiesInViewport := 0
	for _, viewCell := range viewportCells {
		if ids, found := layer[viewCell]; found {
			for _, id := range ids {
				if _, alreadyProcessed := processedEntities[id]; alreadyProcessed {
					continue
				}

				if !onlyClusters {
					// Explicitly copy to avoid raw pointer exposure
					if e, ok := idx.entities[id]; ok {
						eCopy := e
						visible = append(visible, eCopy)
					}
				}
				totalEntitiesInViewport++
				processedEntities[id] = struct{}{}
			}
		}

		if onlyClusters {
			clusterCounts[viewCell] = idx.globalCellCounts[queryResolution][viewCell]
		}
	}

	// 2. O(1) aggregate calculation for out-of-view clusters when zoomed in
	clusters = make(map[string]entity.Cluster)
	if !onlyClusters {
		outOfViewCount := idx.totalGlobalCounts[queryResolution] - totalEntitiesInViewport
		if outOfViewCount > 0 {
			// Pass this as a unified metrics key
			clusters["out_of_view"] = entity.Cluster{
				Count: outOfViewCount,
			}
		}
	}

	// Convert cluster cells to entity.Cluster with centroids (only for zoomed out)
	if onlyClusters {
		for cell, count := range clusterCounts {
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

func (idx *Index) buildClustersPayload(clusterCounts map[h3.Cell]int, viewportCells []h3.Cell, onlyClusters bool) map[string]entity.Cluster {
	clusters := make(map[string]entity.Cluster)
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
	return clusters
}
