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

var targetResolutions = []int{2, 4, 6, 7}

type Index struct {
	mu                sync.RWMutex
	entities          map[string]entity.Entity
	layers            map[int]map[h3.Cell][]string
	globalCellCounts  map[int]map[h3.Cell]int
	totalGlobalCounts map[int]int
	latsBuf           []float64
	lngsBuf           []float64
	r2Buf             []uint64
	r4Buf             []uint64
	r6Buf             []uint64
	r7Buf             []uint64
}

func NewIndex() *Index {
	layers := make(map[int]map[h3.Cell][]string)
	globalCellCounts := make(map[int]map[h3.Cell]int)
	totalGlobalCounts := make(map[int]int)
	for _, res := range targetResolutions {
		layers[res] = make(map[h3.Cell][]string)
		globalCellCounts[res] = make(map[h3.Cell]int)
		totalGlobalCounts[res] = 0
	}
	return &Index{
		entities:          make(map[string]entity.Entity),
		layers:            layers,
		globalCellCounts:  globalCellCounts,
		totalGlobalCounts: totalGlobalCounts,
		latsBuf:           make([]float64, 0),
		lngsBuf:           make([]float64, 0),
		r2Buf:             make([]uint64, 0),
		r4Buf:             make([]uint64, 0),
		r6Buf:             make([]uint64, 0),
		r7Buf:             make([]uint64, 0),
	}
}

type precomputedRemoval struct {
	res  int
	cell h3.Cell
}

func (idx *Index) BatchUpdateRust(entities []entity.Entity, removed []string) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "BatchUpdateRust")

	// Monolithic Write Lock
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// STEP 1: PRE-COMPUTE REMOVALS
	var removalJobs []precomputedRemoval
	if len(removed) > 0 {
		for _, id := range removed {
			if oldEntity, ok := idx.entities[id]; ok {
				latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
				for _, res := range targetResolutions {
					if cell, err := h3.LatLngToCell(latLng, res); err == nil {
						removalJobs = append(removalJobs, precomputedRemoval{res: res, cell: cell})
					}
				}
			}
		}
	}

	// STEP 2: PRE-COMPUTE NEW INDICES VIA NATIVE RUST CORE
	count := len(entities)
	if count > 0 {
		if count > cap(idx.latsBuf) {
			idx.latsBuf = make([]float64, count)
			idx.lngsBuf = make([]float64, count)
			idx.r2Buf = make([]uint64, count)
			idx.r4Buf = make([]uint64, count)
			idx.r6Buf = make([]uint64, count)
			idx.r7Buf = make([]uint64, count)
		}
		lats := idx.latsBuf[:count]
		lngs := idx.lngsBuf[:count]
		r2 := idx.r2Buf[:count]
		r4 := idx.r4Buf[:count]
		r6 := idx.r6Buf[:count]
		r7 := idx.r7Buf[:count]

		for i, e := range entities {
			lats[i] = e.Lat
			lngs[i] = e.Lng
		}

		C.compute_resolutions_batch(
			(*C.double)(unsafe.Pointer(&lats[0])),
			(*C.double)(unsafe.Pointer(&lngs[0])),
			C.size_t(count),
			(*C.uint64_t)(unsafe.Pointer(&r2[0])),
			(*C.uint64_t)(unsafe.Pointer(&r4[0])),
			(*C.uint64_t)(unsafe.Pointer(&r6[0])),
			(*C.uint64_t)(unsafe.Pointer(&r7[0])),
		)
	}

	// Exec Deletions
	for _, job := range removalJobs {
		ids := idx.layers[job.res][job.cell]
		for _, id := range removed {
			for i, existingID := range ids {
				if existingID == id {
					lastIdx := len(ids) - 1
					ids[i] = ids[lastIdx]
					ids = ids[:lastIdx]
					idx.layers[job.res][job.cell] = ids
					idx.globalCellCounts[job.res][job.cell]--
					idx.totalGlobalCounts[job.res]--
					break
				}
			}
		}
	}
	for _, id := range removed {
		delete(idx.entities, id)
	}

	// Exec Insertions / Updates
	for i, e := range entities {
		oldEntity, exists := idx.entities[e.ID]
		if exists {
			latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				if oldCell, oldErr := h3.LatLngToCell(latLng, res); oldErr == nil {
					ids := idx.layers[res][oldCell]
					for j, existingID := range ids {
						if existingID == e.ID {
							lastIdx := len(ids) - 1
							ids[j] = ids[lastIdx]
							ids = ids[:lastIdx]
							idx.layers[res][oldCell] = ids
							idx.globalCellCounts[res][oldCell]--
							idx.totalGlobalCounts[res]--
							break
						}
					}
				}
			}
		}

		idx.entities[e.ID] = e

		// Use persistent buffers for new cells (using r2...r7)
		newCells := [4]h3.Cell{h3.Cell(idx.r2Buf[i]), h3.Cell(idx.r4Buf[i]), h3.Cell(idx.r6Buf[i]), h3.Cell(idx.r7Buf[i])}
		for j, res := range targetResolutions {
			cell := newCells[j]
			if cell == 0 {
				continue
			}
			
			ids := idx.layers[res][cell]
			exists := false
			for _, id := range ids {
				if id == e.ID {
					exists = true
					break
				}
			}
			
			if !exists {
				idx.layers[res][cell] = append(ids, e.ID)
				idx.globalCellCounts[res][cell]++
				idx.totalGlobalCounts[res]++
			}
		}
	}
}

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
	onlyClusters := vp.Zoom < 6

	processedEntities := make(map[string]struct{})
	viewportCellSet := make(map[h3.Cell]struct{})
	for _, vc := range viewportCells {
		viewportCellSet[vc] = struct{}{}
	}

	layer := idx.layers[queryResolution]
	totalEntitiesInViewport := 0
	for viewCell := range viewportCellSet {
		if ids, found := layer[viewCell]; found {
			for i := 0; i < len(ids); i++ {
				id := ids[i]
				if _, alreadyProcessed := processedEntities[id]; alreadyProcessed {
					continue
				}

				if !onlyClusters {
					if e, ok := idx.entities[id]; ok {
						visible = append(visible, e)
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

	clusters = make(map[string]entity.Cluster)
	if !onlyClusters {
		outOfViewCount := idx.totalGlobalCounts[queryResolution] - totalEntitiesInViewport
		if outOfViewCount > 0 {
			clusters["out_of_view"] = entity.Cluster{Count: outOfViewCount}
		}
	}

	if onlyClusters {
		for cell, count := range clusterCounts {
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
