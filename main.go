package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/saptreekly/OSINT/entity"
	"github.com/saptreekly/OSINT/seeder"
	"github.com/saptreekly/OSINT/server"
	"github.com/saptreekly/OSINT/store"
)

func main() {
	// Parse environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	minPushIntervalStr := os.Getenv("MIN_PUSH_INTERVAL")
	minPushInterval := 500 * time.Millisecond
	if minPushIntervalStr != "" {
		if ms, err := strconv.Atoi(minPushIntervalStr); err == nil {
			minPushInterval = time.Duration(ms) * time.Millisecond
		}
	}

	// Create store and start background poller
	s := store.NewStore()

	// Start OpenSky seeder
	openSkySeeder := seeder.NewOpenSkySeeder()
	ctx, cancel := context.WithCancel(context.Background())
	go seeder.Run(ctx, openSkySeeder, func(entities []entity.Entity) {
		s.Apply(entities)
	})

	// Create HTTP server
	srv := server.NewServer(
		fmt.Sprintf(":%s", port),
		s,
		minPushInterval,
	)

	// Start server in a goroutine
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown
	log.Println("Shutting down...")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}
