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
	latsBuf, lngsBuf                   []float64
	res2Buf, res4Buf, res6Buf, res7Buf []uint64
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

type precomputedRemoval struct {
	res  int
	cell h3.Cell
}

func (idx *Index) BatchUpdateRust(entities []entity.Entity, removed []string) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "BatchUpdateRust")

	// STEP 1: PRE-COMPUTE REMOVAL CELLS (READ LOCK ONLY, NO WRITE LOCK!)
	var removalJobs []precomputedRemoval
	if len(removed) > 0 {
		idx.mu.RLock()
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
		idx.mu.RUnlock()
	}

	// STEP 2: PRE-COMPUTE NEW INDICES VIA RUST NATIVE CORE (NO LOCKS!)
	count := len(entities)
	var r2, r4, r6, r7 []uint64
	if count > 0 {
		r2 = make([]uint64, count)
		r4 = make([]uint64, count)
		r6 = make([]uint64, count)
		r7 = make([]uint64, count)
		lats := make([]float64, count)
		lngs := make([]float64, count)

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

	// STEP 3: ATOMIC MEMORY MUTATION GATE (ONE TIGHT, FAST WRITE LOCK!)
	idx.mu.Lock()
	defer idx.mu.Unlock()

	removedSet := make(map[string]struct{}, len(removed))
	for _, id := range removed {
		removedSet[id] = struct{}{}
	}

	// Instant Deletions
	for _, job := range removalJobs {
		idx.globalCellCounts[job.res][job.cell]--
		if idx.globalCellCounts[job.res][job.cell] == 0 {
			delete(idx.globalCellCounts[job.res], job.cell)
		}
		idx.totalGlobalCounts[job.res]--

		if ids, found := idx.layers[job.res][job.cell]; found {
			for i := 0; i < len(ids); {
				if _, isRemoved := removedSet[ids[i]]; isRemoved {
					lastIdx := len(ids) - 1
					ids[i] = ids[lastIdx]
					ids = ids[:lastIdx]
				} else {
					i++
				}
			}
			idx.layers[job.res][job.cell] = ids
			if len(idx.layers[job.res][job.cell]) == 0 {
				delete(idx.layers[job.res], job.cell)
			}
		}
	}
	for _, id := range removed {
		delete(idx.entities, id)
	}

	// Instant Insertions / Updates
	for i, e := range entities {
		oldEntity, exists := idx.entities[e.ID]
		if exists {
			latLng := h3.LatLng{Lat: oldEntity.Lat, Lng: oldEntity.Lng}
			for _, res := range targetResolutions {
				if oldCell, oldErr := h3.LatLngToCell(latLng, res); oldErr == nil {
					idx.globalCellCounts[res][oldCell]--
					if idx.globalCellCounts[res][oldCell] == 0 {
						delete(idx.globalCellCounts[res], oldCell)
					}
					idx.totalGlobalCounts[res]--

					if ids, found := idx.layers[res][oldCell]; found {
						for j := 0; j < len(ids); {
							if ids[j] == e.ID {
								lastIdx := len(ids) - 1
								ids[j] = ids[lastIdx]
								ids = ids[:lastIdx]
							} else {
								j++
							}
						}
						idx.layers[res][oldCell] = ids
						if len(idx.layers[res][oldCell]) == 0 {
							delete(idx.layers[res], oldCell)
						}
					}
				}
			}
		}

		idx.entities[e.ID] = e

		newCells := [4]h3.Cell{h3.Cell(r2[i]), h3.Cell(r4[i]), h3.Cell(r6[i]), h3.Cell(r7[i])}
		for j, res := range targetResolutions {
			cell := newCells[j]
			if cell == 0 {
				continue
			}
			idx.globalCellCounts[res][cell]++
			idx.totalGlobalCounts[res]++
			idx.layers[res][cell] = append(idx.layers[res][cell], e.ID)
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
	for _, viewCell := range viewportCells {
		if ids, found := layer[viewCell]; found {
			for _, id := range ids {
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
