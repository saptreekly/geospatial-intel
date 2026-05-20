package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/util"
)

func TestRecordHistoryPerformanceLogging(t *testing.T) {
	util.InitPerfLogger("../performance_logs.md")
	s := NewStore()
	defer os.Remove("osint.db")

	// Generate a massive amount of entities to force a slow transaction
	count := 50000
	entities := make([]entity.Entity, count)
	for i := 0; i < count; i++ {
		entities[i] = entity.Entity{
			ID:        fmt.Sprintf("e%d", i),
			UpdatedAt: time.Now().Unix(),
			Lat:       0.0,
			Lng:       0.0,
		}
	}

	// This should trigger the critical logging threshold
	s.recordHistory(entities)

	// Verify log file content
	content, err := os.ReadFile("performance_logs.md")
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if len(content) == 0 {
		t.Error("Expected performance logs to be populated, but file is empty")
	}
}
