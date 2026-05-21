package spatial

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	"github.com/saptreekly/geospatial-intel/entity"
)

func BenchmarkBatchUpdateRust(b *testing.B) {
	loads := []int{100, 5000, 50000}
	for _, load := range loads {
		b.Run(fmt.Sprintf("Load-%d", load), func(b *testing.B) {
			idx := NewIndex()
			// Pre-generate entities for this load to avoid allocation inside the timed loop
			entities := make([]entity.Entity, load)
			for i := 0; i < load; i++ {
				entities[i] = entity.Entity{
					ID:  strconv.Itoa(i),
					Lat: rand.Float64()*179.8 - 89.9,
					Lng: rand.Float64()*359.8 - 179.9,
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// We stop the timer if we were doing any Go-side setup here,
				// but since entities are pre-generated, we can just call the function.
				// However, the request specifically asked for timer gates to isolate FFI.
				b.StartTimer()
				idx.BatchUpdateRust(entities, nil)
				b.StopTimer()
			}
		})
	}
}

func BenchmarkQuery(b *testing.B) {
	loads := []int{100, 5000, 50000}
	for _, load := range loads {
		b.Run(fmt.Sprintf("Load-%d", load), func(b *testing.B) {
			idx := NewIndex()
			entities := make([]entity.Entity, load)
			for i := 0; i < load; i++ {
				entities[i] = entity.Entity{
					ID:  strconv.Itoa(i),
					Lat: rand.Float64()*179.8 - 89.9,
					Lng: rand.Float64()*359.8 - 179.9,
				}
			}
			idx.BatchUpdateRust(entities, nil)

			vp := entity.Viewport{North: 90, South: -90, East: 180, West: -180, Zoom: 7}

			outVisible := make([]entity.Entity, 0, load)
			outClusters := make(map[string]entity.Cluster)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outVisible = outVisible[:0]
				for k := range outClusters {
					delete(outClusters, k)
				}
				_, _ = idx.Query(vp, outVisible, outClusters)
			}
		})
	}
}
