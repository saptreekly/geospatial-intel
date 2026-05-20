package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/store"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestServer_WebSocketPipelineE2E(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Setup local test server
	s := store.NewStore()
	// Use port 0 to let the OS pick a random free port
	srv := NewServer("localhost:0", s, 0)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// 2. Connect WebSocket client
	// Convert http URL to ws URL
	u := "ws" + ts.URL[len("http"):] + "/stream"
	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		t.Fatalf("Failed to dial WebSocket: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "closing")

	// 3. Send Viewport - MUST be sent before Apply to ensure the hub sees it
	vp := map[string]interface{}{
		"type":  "viewport",
		"north": 90.0,
		"south": -90.0,
		"east":  180.0,
		"west":  -180.0,
		"zoom":  7,
	}
	if err := wsjson.Write(ctx, c, vp); err != nil {
		t.Fatalf("Failed to write viewport: %v", err)
	}
	
	// Ensure server processed viewport
	time.Sleep(50 * time.Millisecond)

	// 4. Inject mock entity
	e := entity.Entity{
		ID:        "test-plane",
		Lat:       0.0,
		Lng:       0.0,
		UpdatedAt: time.Now().Unix(),
	}
	s.Apply([]entity.Entity{e})

	// 5. Read delta
	var delta entity.Delta
	// Set a short timeout for the read
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	
	if err := wsjson.Read(readCtx, c, &delta); err != nil {
		t.Fatalf("Failed to read delta: %v", err)
	}

	// 6. Assertions
	if len(delta.Added) == 0 || delta.Added[0].ID != "test-plane" {
		t.Errorf("Expected added entity 'test-plane', got %v", delta.Added)
	}
}
