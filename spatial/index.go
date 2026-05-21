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
    uint64_t* out_res3,
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

var targetResolutions = []int{2, 3, 4, 6, 7}

var (
	visiblePool           = sync.Pool{New: func() interface{} { return make([]entity.Entity, 0, 1024) }}
	clusterCountsPool     = sync.Pool{New: func() interface{} { return make(map[h3.Cell]int, 1024) }}
	processedEntitiesPool = sync.Pool{New: func() interface{} { return make(map[uint32]struct{}, 4096) }}
	viewportCellSetPool   = sync.Pool{New: func() interface{} { return make(map[h3.Cell]struct{}, 1024) }}
)

type Index struct {
	mu                sync.RWMutex
	entities          map[string]entity.Entity
	layers            map[int]map[h3.Cell][]uint32
	globalCellCounts  map[int]map[h3.Cell]int
	totalGlobalCounts map[int]int

	idCounter            uint32
	entityIDToInternalID map[string]uint32
	idToEntityID         map[uint32]string
	entityCells          map[uint32][5]h3.Cell

	latsBuf []float64
	lngsBuf []float64
	r2Buf   []uint64
	r3Buf   []uint64
	r4Buf   []uint64
	r6Buf   []uint64
	r7Buf   []uint64
}

func NewIndex() *Index {
	layers := make(map[int]map[h3.Cell][]uint32)
	globalCellCounts := make(map[int]map[h3.Cell]int)
	totalGlobalCounts := make(map[int]int)
	for _, res := range targetResolutions {
		layers[res] = make(map[h3.Cell][]uint32)
		globalCellCounts[res] = make(map[h3.Cell]int)
		totalGlobalCounts[res] = 0
	}
	return &Index{
		entities:          make(map[string]entity.Entity),
		layers:            layers,
		globalCellCounts:  globalCellCounts,
		totalGlobalCounts: totalGlobalCounts,

		entityIDToInternalID: make(map[string]uint32),
		idToEntityID:         make(map[uint32]string),
		entityCells:          make(map[uint32][5]h3.Cell),

		latsBuf: make([]float64, 0),
		lngsBuf: make([]float64, 0),
		r2Buf:   make([]uint64, 0),
		r3Buf:   make([]uint64, 0),
		r4Buf:   make([]uint64, 0),
		r6Buf:   make([]uint64, 0),
		r7Buf:   make([]uint64, 0),
	}
}

type precomputedRemoval struct {
	res  int
	cell h3.Cell
}

func (idx *Index) getInternalID(id string) uint32 {
	if internal, ok := idx.entityIDToInternalID[id]; ok {
		return internal
	}
	idx.idCounter++
	internal := idx.idCounter
	idx.entityIDToInternalID[id] = internal
	idx.idToEntityID[internal] = id
	return internal
}

func (idx *Index) BatchUpdateRust(entities []entity.Entity, removed []string) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "BatchUpdateRust")

	// Monolithic Write Lock
	idx.mu.Lock()
	defer idx.mu.Unlock()

	count := len(entities)
	if len(idx.entities) == 0 && count > 0 {
		// Pre-size map bucket tables to completely eliminate bucket splitting and rehashing churn
		idx.entities = make(map[string]entity.Entity, count)
		idx.entityIDToInternalID = make(map[string]uint32, count)
		idx.entityCells = make(map[uint32][5]h3.Cell, count)
	}

	// STEP 1: PRE-COMPUTE REMOVALS
	var removalJobs []precomputedRemoval
	if len(removed) > 0 {
		for _, id := range removed {
			if internalID, ok := idx.entityIDToInternalID[id]; ok {
				if cells, ok := idx.entityCells[internalID]; ok {
					for j, res := range targetResolutions {
						removalJobs = append(removalJobs, precomputedRemoval{res: res, cell: cells[j]})
					}
				}
			}
		}
	}

	// STEP 2: PRE-COMPUTE NEW INDICES VIA NATIVE RUST CORE
	if count > 0 {
		if count > cap(idx.latsBuf) {
			idx.latsBuf = make([]float64, count)
			idx.lngsBuf = make([]float64, count)
			idx.r2Buf = make([]uint64, count)
			idx.r3Buf = make([]uint64, count)
			idx.r4Buf = make([]uint64, count)
			idx.r6Buf = make([]uint64, count)
			idx.r7Buf = make([]uint64, count)
		}
		lats := idx.latsBuf[:count]
		lngs := idx.lngsBuf[:count]
		r2 := idx.r2Buf[:count]
		r3 := idx.r3Buf[:count]
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
			(*C.uint64_t)(unsafe.Pointer(&r3[0])),
			(*C.uint64_t)(unsafe.Pointer(&r4[0])),
			(*C.uint64_t)(unsafe.Pointer(&r6[0])),
			(*C.uint64_t)(unsafe.Pointer(&r7[0])),
		)
	}

	// Exec Deletions
	for _, job := range removalJobs {
		ids := idx.layers[job.res][job.cell]
		for _, id := range removed {
			internalID, ok := idx.entityIDToInternalID[id]
			if !ok {
				continue
			}
			for i, existingID := range ids {
				if existingID == internalID {
					lastIdx := len(ids) - 1
					ids[i] = ids[lastIdx]
					ids = ids[:lastIdx]
					if len(ids) == 0 {
						delete(idx.layers[job.res], job.cell)
					} else {
						idx.layers[job.res][job.cell] = ids
					}
					idx.globalCellCounts[job.res][job.cell]--
					idx.totalGlobalCounts[job.res]--
					break
				}
			}
		}
	}
	for _, id := range removed {
		if internalID, ok := idx.entityIDToInternalID[id]; ok {
			delete(idx.entityCells, internalID)
		}
		delete(idx.entities, id)
		delete(idx.entityIDToInternalID, id)
	}

	// Exec Insertions / Updates
	for i, e := range entities {
		_, exists := idx.entities[e.ID]
		newCells := [5]h3.Cell{h3.Cell(idx.r2Buf[i]), h3.Cell(idx.r3Buf[i]), h3.Cell(idx.r4Buf[i]), h3.Cell(idx.r6Buf[i]), h3.Cell(idx.r7Buf[i])}
		internalID := idx.getInternalID(e.ID)

		if exists {
			oldCells := idx.entityCells[internalID]
			for j, res := range targetResolutions {
				oldCell := oldCells[j]
				newCell := newCells[j]
				if newCell == 0 {
					continue
				}

				if oldCell != newCell {
					// Remove from old cell
					ids := idx.layers[res][oldCell]
					for k, id := range ids {
						if id == internalID {
							lastIdx := len(ids) - 1
							ids[k] = ids[lastIdx]
							ids = ids[:lastIdx]
							if len(ids) == 0 {
								delete(idx.layers[res], oldCell)
							} else {
								idx.layers[res][oldCell] = ids
							}
							idx.globalCellCounts[res][oldCell]--
							idx.totalGlobalCounts[res]--
							break
						}
					}
					// Add to new cell
					if len(idx.layers[res][newCell]) == 0 {
						idx.layers[res][newCell] = make([]uint32, 0, 8)
					}
					idx.layers[res][newCell] = append(idx.layers[res][newCell], internalID)
					idx.globalCellCounts[res][newCell]++
					idx.totalGlobalCounts[res]++
				}
			}
		} else {
			// New entity
			for j, res := range targetResolutions {
				cell := newCells[j]
				if cell == 0 {
					continue
				}
				if len(idx.layers[res][cell]) == 0 {
					idx.layers[res][cell] = make([]uint32, 0, 8)
				}
				idx.layers[res][cell] = append(idx.layers[res][cell], internalID)
				idx.globalCellCounts[res][cell]++
				idx.totalGlobalCounts[res]++
			}
		}
		idx.entityCells[internalID] = newCells
		idx.entities[e.ID] = e
	}
}

func (idx *Index) Query(vp entity.Viewport, outVisible []entity.Entity, outClusters map[string]entity.Cluster) ([]entity.Entity, error) {
	start := time.Now()
	defer util.LogIfSlow(start, 10*time.Millisecond, "Query")

	queryResolution := ZoomToResolution(vp.Zoom)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Get from pools
	visible := visiblePool.Get().([]entity.Entity)
	clusterCounts := clusterCountsPool.Get().(map[h3.Cell]int)
	processedEntities := processedEntitiesPool.Get().(map[uint32]struct{})
	viewportCellSet := viewportCellSetPool.Get().(map[h3.Cell]struct{})

	// Defer cleanup and put back
	defer func() {
		visible = visible[:0]
		visiblePool.Put(visible)

		for k := range clusterCounts {
			delete(clusterCounts, k)
		}
		clusterCountsPool.Put(clusterCounts)

		for k := range processedEntities {
			delete(processedEntities, k)
		}
		processedEntitiesPool.Put(processedEntities)

		for k := range viewportCellSet {
			delete(viewportCellSet, k)
		}
		viewportCellSetPool.Put(viewportCellSet)
	}()

	onlyClusters := vp.Zoom < 5

	if !vp.IsGlobal() {
		viewportCells, err := ViewportToCells(vp, queryResolution)
		if err != nil {
			return nil, err
		}
		for _, vc := range viewportCells {
			viewportCellSet[vc] = struct{}{}
		}
	}

	layer := idx.layers[queryResolution]
	totalEntitiesInViewport := 0

	if vp.IsGlobal() {
		// Iterate over populated cells only
		for populatedCell, ids := range layer {
			for i := 0; i < len(ids); i++ {
				internalID := ids[i]
				if _, alreadyProcessed := processedEntities[internalID]; alreadyProcessed {
					continue
				}

				if !onlyClusters {
					stringID := idx.idToEntityID[internalID]
					if e, ok := idx.entities[stringID]; ok {
						visible = append(visible, e)
					}
				}
				totalEntitiesInViewport++
				processedEntities[internalID] = struct{}{}
			}
			if onlyClusters {
				clusterCounts[populatedCell] = idx.globalCellCounts[queryResolution][populatedCell]
			}
		}
	} else if len(viewportCellSet) < len(layer) {
		// Iterate over viewport
		for viewCell := range viewportCellSet {
			if ids, found := layer[viewCell]; found {
				for i := 0; i < len(ids); i++ {
					internalID := ids[i]
					if _, alreadyProcessed := processedEntities[internalID]; alreadyProcessed {
						continue
					}

					if !onlyClusters {
						stringID := idx.idToEntityID[internalID]
						if e, ok := idx.entities[stringID]; ok {
							visible = append(visible, e)
						}
					}
					totalEntitiesInViewport++
					processedEntities[internalID] = struct{}{}
				}
			}

			if onlyClusters {
				clusterCounts[viewCell] = idx.globalCellCounts[queryResolution][viewCell]
			}
		}
	} else {
		// Iterate over populated cells
		for populatedCell, ids := range layer {
			if _, inView := viewportCellSet[populatedCell]; inView {
				for i := 0; i < len(ids); i++ {
					internalID := ids[i]
					if _, alreadyProcessed := processedEntities[internalID]; alreadyProcessed {
						continue
					}

					if !onlyClusters {
						stringID := idx.idToEntityID[internalID]
						if e, ok := idx.entities[stringID]; ok {
							visible = append(visible, e)
						}
					}
					totalEntitiesInViewport++
					processedEntities[internalID] = struct{}{}
				}
			}
			if onlyClusters {
				clusterCounts[populatedCell] = idx.globalCellCounts[queryResolution][populatedCell]
			}
		}
	}

	if !onlyClusters {
		outOfViewCount := idx.totalGlobalCounts[queryResolution] - totalEntitiesInViewport
		if outOfViewCount > 0 {
			outClusters["out_of_view"] = entity.Cluster{Count: outOfViewCount}
		}
	}

	if onlyClusters {
		for cell, count := range clusterCounts {
			if count > 0 {
				latLng, _ := h3.CellToLatLng(cell)
				outClusters[cell.String()] = entity.Cluster{
					Lat:   latLng.Lat,
					Lng:   latLng.Lng,
					Count: count,
				}
			}
		}
	}

	outVisible = append(outVisible, visible...)
	return outVisible, nil
}
