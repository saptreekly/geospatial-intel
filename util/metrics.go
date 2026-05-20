package util

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

var (
	perfFile *os.File
	perfMu   sync.Mutex
)

// InitPerfLogger initializes the performance log file.
func InitPerfLogger(path string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open %s: %v", path, err)
	}
	perfMu.Lock()
	defer perfMu.Unlock()
	perfFile = f
}

// LogIfSlow checks if duration exceeds threshold and logs if so.
func LogIfSlow(start time.Time, threshold time.Duration, message string) {
	elapsed := time.Since(start)
	if elapsed > threshold {
		msg := fmt.Sprintf("PERF WARNING: %s took %v (threshold: %v)", message, elapsed, threshold)
		LogPerformance(msg)
	}
}

// LogPerformance appends a performance metric message to the log file.
func LogPerformance(message string) {
	perfMu.Lock()
	defer perfMu.Unlock()
	if perfFile == nil {
		return // Ignore if not initialized
	}

	_, err := perfFile.WriteString(message + "\n")
	if err != nil {
		log.Printf("Failed to write to performance_logs.md: %v", err)
	} else {
		perfFile.Sync()
	}
}
