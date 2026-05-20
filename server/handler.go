package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/util"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// StreamHandler handles a WebSocket /stream connection.
func StreamHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, h *Hub, minPushInterval time.Duration) {
	start := time.Now()
	defer util.LogIfSlow(start, 500*time.Millisecond, "StreamHandler (Connection Duration)")

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		log.Printf("WebSocket accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "server error")

	client := NewClient(minPushInterval)
	h.Register(client)
	log.Printf("Client registered")
	defer h.Unregister(client)

	// Set up ping/pong for keepalive
	c.SetReadLimit(32 * 1024) // 32KB max message size
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Ping(ctx); err != nil {
					return
				}
			}
		}
	}()

	// Read viewport messages in a goroutine
	viewportChan := make(chan entity.Viewport)
	go handleClientViewportUpdates(ctx, c, viewportChan)

	// Main loop: send deltas, receive viewport updates
	for {
		select {
		case <-ctx.Done():
			return
		case vp, ok := <-viewportChan:
			if !ok {
				return
			}
			h.HandleViewportUpdate(client, vp)
		case deltaBytes, ok := <-client.ch:
			if !ok {
				return
			}
			if err := c.Write(ctx, websocket.MessageText, deltaBytes); err != nil {
				return
			}
		}
	}
}

func handleClientViewportUpdates(ctx context.Context, c *websocket.Conn, viewportChan chan<- entity.Viewport) {
	defer close(viewportChan)
	for {
		var msg struct {
			Type  string  `json:"type"`
			North float64 `json:"north"`
			South float64 `json:"south"`
			East  float64 `json:"east"`
			West  float64 `json:"west"`
			Zoom  int     `json:"zoom"`
		}
		if err := wsjson.Read(ctx, c, &msg); err != nil {
			return
		}
		if msg.Type == "viewport" {
			viewportChan <- entity.Viewport{
				North: msg.North,
				South: msg.South,
				East:  msg.East,
				West:  msg.West,
				Zoom:  msg.Zoom,
			}
		}
	}
}
