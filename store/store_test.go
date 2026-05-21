package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/util"
)

func TestRecordHistoryPerformanceLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "performance_logs.md")
	util.InitPerfLogger(logPath)
	s := NewStore()
	defer os.Remove("osint.db")

	// Generate a massive amount of entities to force a slow transaction
	count := 500000
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
	s.db.Close() // Flush any remaining state and close file handles

	// Verify log file content
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if len(content) == 0 {
		t.Error("Expected performance logs to be populated, but file is empty")
	}
}
