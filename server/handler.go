package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/saptreekly/OSINT/entity"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// StreamHandler handles a WebSocket /stream connection.
func StreamHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, h *Hub, minPushInterval time.Duration) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		log.Printf("WebSocket accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "server error")

	client := NewClient(minPushInterval)
	h.Register(client)
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
	go func() {
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
				close(viewportChan)
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
	}()

	// Main loop: send deltas, receive viewport updates
	for {
		select {
		case <-ctx.Done():
			return
		case vp, ok := <-viewportChan:
			if !ok {
				return
			}
			client.viewport = vp
		case delta, ok := <-client.ch:
			if !ok {
				return
			}
			if err := wsjson.Write(ctx, c, delta); err != nil {
				return
			}
		}
	}
}
