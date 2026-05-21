package spatial

import (
	"fmt"
	"math/rand"
	"os"
	"runtime/trace"
	"sync"
	"testing"

	"github.com/saptreekly/geospatial-intel/entity"
)

func TestTraceHighLoadSimulation(t *testing.T) {
	f, err := os.Create("trace.out")
	if err != nil {
		t.Fatalf("failed to create trace output file: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("failed to close trace file: %v", err)
		}
	}()

	if err := trace.Start(f); err != nil {
		t.Fatalf("failed to start trace: %v", err)
	}
	defer trace.Stop()

	idx := NewIndex()
	const entityCount = 50000
	const readWorkers = 10
	const writeTicks = 5

	// Pre-generate data
	entities := make([]entity.Entity, entityCount)
	for i := 0; i < entityCount; i++ {
		entities[i] = entity.Entity{
			ID:  fmt.Sprintf("target-%d", i),
			Lat: rand.Float64()*179.8 - 89.9,
			Lng: rand.Float64()*359.8 - 179.9,
		}
	}

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// Start concurrent read streams
	for i := 0; i < readWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vp := entity.Viewport{North: 90, South: -90, East: 180, West: -180, Zoom: 7}
			for {
				select {
				case <-stopChan:
					return
				default:
					visible := make([]entity.Entity, 0, 100)
					clusters := make(map[string]entity.Cluster)
					_, _ = idx.Query(vp, visible, clusters)
				}
			}
		}(i)
	}

	// Execute continuous burst of writes
	for i := 0; i < writeTicks; i++ {
		// Update coordinates slightly to simulate movement
		for j := range entities {
			entities[j].Lat += (rand.Float64() - 0.5) * 0.001
			entities[j].Lng += (rand.Float64() - 0.5) * 0.001
		}
		idx.BatchUpdateRust(entities, nil)
	}

	close(stopChan)
	wg.Wait()
}
