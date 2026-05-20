package spatial

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/saptreekly/geospatial-intel/entity"
)

func BenchmarkBatchUpdateRust(b *testing.B) {
	loads := []int{100, 5000, 50000}
	for _, load := range loads {
		b.Run("Load-"+strconv.Itoa(load), func(b *testing.B) {
			idx := NewIndex()
			// Pre-generate entities for this load
			entities := make([]entity.Entity, load)
			for i := 0; i < load; i++ {
				entities[i] = entity.Entity{
					ID:  strconv.Itoa(i),
					Lat: rand.Float64()*180 - 90,
					Lng: rand.Float64()*360 - 180,
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx.BatchUpdateRust(entities, nil)
			}
		})
	}
}

func BenchmarkQuery(b *testing.B) {
	loads := []int{100, 5000, 50000}
	for _, load := range loads {
		b.Run("Load-"+strconv.Itoa(load), func(b *testing.B) {
			idx := NewIndex()
			entities := make([]entity.Entity, load)
			for i := 0; i < load; i++ {
				entities[i] = entity.Entity{
					ID:  strconv.Itoa(i),
					Lat: rand.Float64()*180 - 90,
					Lng: rand.Float64()*360 - 180,
				}
			}
			idx.BatchUpdateRust(entities, nil)
			
			vp := entity.Viewport{North: 90, South: -90, East: 180, West: -180, Zoom: 7}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = idx.Query(vp)
			}
		})
	}
}
