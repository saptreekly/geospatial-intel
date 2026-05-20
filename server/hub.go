package server

import (
	"log"
	"sync"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/store"
)

// Client represents a connected WebSocket client.
type Client struct {
	ch              chan entity.Delta
	viewport        entity.Viewport
	seen            map[string]uint64 // entity ID → version last sent to client
	lastPush        time.Time
	minPushInterval time.Duration
}

// Hub manages all connected clients and broadcasts events.
type Hub struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
	store   *store.Store
	sub     *store.Subscription
}

var deltaPool = sync.Pool{
	New: func() any {
		return &entity.Delta{
			Added:    make([]entity.Entity, 0, 100),
			Updated:  make([]entity.Entity, 0, 100),
			Removed:  make([]string, 0, 100),
			Clusters: make(map[string]entity.Cluster),
		}
	},
}

// NewHub creates a new Hub.
func NewHub(s *store.Store) *Hub {
	h := &Hub{
		clients: make(map[*Client]struct{}),
		store:   s,
		sub:     s.Subscribe(),
	}
	go h.broadcast()
	return h
}

// NewClient creates a new client connection.
func NewClient(minPushInterval time.Duration) *Client {
	return &Client{
		ch:              make(chan entity.Delta, 1),
		seen:            make(map[string]uint64),
		minPushInterval: minPushInterval,
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	close(c.ch)
}

// broadcast listens for store events and pushes deltas to all clients.
func (h *Hub) broadcast() {
	for event := range h.sub.C() {
		h.mu.Lock()
		clients := make([]*Client, 0, len(h.clients))
		for c := range h.clients {
			clients = append(clients, c)
		}
		h.mu.Unlock()

		start := time.Now()
		clientsSwept := 0
		totalDeltaPayloadSize := 0

		for _, c := range clients {
			// Compute delta for this client
			visible, clusters, err := h.store.Query(c.viewport)
			if err != nil {
				continue
			}

			// Rate-limit pushes
			if time.Since(c.lastPush) < c.minPushInterval {
				continue
			}

			// Compute delta
			delta := deltaPool.Get().(*entity.Delta)
			delta.Seq = event.Seq
			delta.Added = delta.Added[:0]
			delta.Updated = delta.Updated[:0]
			delta.Removed = delta.Removed[:0]
			// Clear existing map entries without reallocating
			for k := range delta.Clusters {
				delete(delta.Clusters, k)
			}
			delta.Clusters = clusters

			// Categorize changes
			visibleSet := make(map[string]struct{})
			for _, e := range visible {
				visibleSet[e.ID] = struct{}{}
				lastVersion, seen := c.seen[e.ID]
				if !seen {
					delta.Added = append(delta.Added, e)
					c.seen[e.ID] = e.Version
				} else if lastVersion < e.Version {
					delta.Updated = append(delta.Updated, e)
					c.seen[e.ID] = e.Version
				}
			}

			// Compute removed entities (in seen but not in visible)
			for id := range c.seen {
				if _, ok := visibleSet[id]; !ok {
					delta.Removed = append(delta.Removed, id)
					delete(c.seen, id)
				}
			}

			// Skip if no changes
			if len(delta.Added) == 0 && len(delta.Updated) == 0 && len(delta.Removed) == 0 && len(delta.Clusters) == 0 {
				deltaPool.Put(delta)
				continue
			}

			// Payload size tracker
			totalDeltaPayloadSize += len(delta.Added) + len(delta.Updated) + len(delta.Removed) + len(delta.Clusters)
			clientsSwept++

			// Send delta
			select {
			case c.ch <- *delta: // Need to pass a value, not a pointer, as ch is chan entity.Delta
				c.lastPush = time.Now()
				deltaPool.Put(delta)
			default:
				// Client ch is full; skip this update
				deltaPool.Put(delta)
			}
		}

		elapsed := time.Since(start)
		if elapsed > 50*time.Millisecond {
			log.Printf("WARNING: Broadcast loop slow: %v, clients swept: %d, total delta entities: %d", elapsed, clientsSwept, totalDeltaPayloadSize)
		}
	}
}
