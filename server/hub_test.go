package server

import (
	"testing"
	"time"
	"github.com/jackweekly/OSINT/entity"
	"github.com/jackweekly/OSINT/store"
)

func TestHub_BroadcastOptimization(t *testing.T) {
	s := store.NewStore()
	h := NewHub(s)
	
	client := NewClient(0 * time.Millisecond) // Disable rate limiting
	client.viewport = entity.Viewport{
		North: 1.0, South: -1.0, East: 1.0, West: -1.0, Zoom: 7,
	}
	h.Register(client)

	// Add an entity
	e := entity.Entity{ID: "e1", Lat: 0.0, Lng: 0.0, Version: 1}
	s.Apply([]entity.Entity{e})

	// Wait for broadcast
	select {
	case delta := <-client.ch:
		if len(delta.Added) != 1 || delta.Added[0].ID != "e1" {
			t.Errorf("Expected delta.Added to contain e1, got %+v", delta.Added)
		}
		if delta.Updated != nil {
			t.Errorf("Expected delta.Updated to be nil, got %v", delta.Updated)
		}
		if delta.Removed != nil {
			t.Errorf("Expected delta.Removed to be nil, got %v", delta.Removed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timed out waiting for broadcast")
	}
}
