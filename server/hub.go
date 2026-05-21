package server

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/store"
	"github.com/saptreekly/geospatial-intel/util"
)

// Client represents a connected WebSocket client.
type Client struct {
	ch              chan []byte
	viewportMu      sync.RWMutex
	viewport        entity.Viewport
	seen            map[string]uint64 // entity ID → version last sent to client
	lastPush        time.Time
	minPushInterval time.Duration
}

// SetViewport safely updates the client's viewport.
func (c *Client) SetViewport(vp entity.Viewport) {
	c.viewportMu.Lock()
	defer c.viewportMu.Unlock()
	c.viewport = vp
}

// GetViewport safely retrieves the client's current viewport.
func (c *Client) GetViewport() entity.Viewport {
	c.viewportMu.RLock()
	defer c.viewportMu.RUnlock()
	return c.viewport
}

// IsEligible checks if a client is eligible for a push.
func (c *Client) IsEligible() bool {
	c.viewportMu.RLock()
	defer c.viewportMu.RUnlock()
	return time.Since(c.lastPush) >= c.minPushInterval
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

var queryBufferPool = sync.Pool{
	New: func() any {
		return &struct {
			Visible  []entity.Entity
			Clusters map[string]entity.Cluster
		}{
			Visible:  make([]entity.Entity, 0, 5000),
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
		ch:              make(chan []byte, 1),
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

		// Group clients by Viewport
		groups := make(map[entity.Viewport][]*Client)
		for _, c := range clients {
			groups[c.GetViewport()] = append(groups[c.GetViewport()], c)
		}

		start := time.Now()
		clientsSwept := 0
		totalDeltaPayloadSize := 0

		for vp, clientsInGroup := range groups {
			// Pre-filter eligible clients for this Viewport
			var eligible []*Client
			for _, c := range clientsInGroup {
				if c.IsEligible() {
					eligible = append(eligible, c)
				}
			}

			if len(eligible) == 0 {
				continue
			}

			// Query once per group
			buf := queryBufferPool.Get().(*struct {
				Visible  []entity.Entity
				Clusters map[string]entity.Cluster
			})
			// Reset the tracking collections cleanly
			buf.Visible = buf.Visible[:0]
			for k := range buf.Clusters {
				delete(buf.Clusters, k)
			}

			// Execute the zero-allocation query pass
			visible, err := h.store.Query(vp, buf.Visible, buf.Clusters)
			if err != nil {
				queryBufferPool.Put(buf)
				continue
			}

			// Compute and send for eligible clients
			for _, c := range eligible {
				sent, payloadSize := h.computeAndSend(c, event, visible, buf.Clusters)
				if sent {
					clientsSwept++
					totalDeltaPayloadSize += payloadSize
				}
			}
			queryBufferPool.Put(buf)
		}

		elapsed := time.Since(start)
		if elapsed > 50*time.Millisecond {
			msg := "WARNING: Broadcast loop slow: " + elapsed.String() + ", clients swept: " + strconv.Itoa(clientsSwept) + ", total delta entities: " + strconv.Itoa(totalDeltaPayloadSize)
			log.Printf("%s", msg)
			util.LogPerformance(msg)
		}
	}
}

func (h *Hub) computeAndSend(c *Client, event store.StoreEvent, visible []entity.Entity, clusters map[string]entity.Cluster) (bool, int) {
	c.viewportMu.Lock()
	defer c.viewportMu.Unlock()

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
		return false, 0
	}

	deltaBytes, err := json.Marshal(delta)
	payloadSize := len(delta.Added) + len(delta.Updated) + len(delta.Removed) + len(delta.Clusters)
	deltaPool.Put(delta)

	if err != nil {
		return false, 0
	}

	// Send delta
	select {
	case c.ch <- deltaBytes:
		c.lastPush = time.Now()
		return true, payloadSize
	default:
		// Client ch is full; skip this update
		return false, 0
	}
}

// HandleViewportUpdate locks and updates the client's viewport, immediately queries the store,
// computes the delta against the client's seen entities, and pushes the delta to the client.
func (h *Hub) HandleViewportUpdate(c *Client, vp entity.Viewport) {
	c.SetViewport(vp)

	buf := queryBufferPool.Get().(*struct {
		Visible  []entity.Entity
		Clusters map[string]entity.Cluster
	})
	// Reset the tracking collections cleanly
	buf.Visible = buf.Visible[:0]
	for k := range buf.Clusters {
		delete(buf.Clusters, k)
	}

	visible, err := h.store.Query(vp, buf.Visible, buf.Clusters)
	if err != nil {
		queryBufferPool.Put(buf)
		return
	}

	c.viewportMu.Lock()
	defer c.viewportMu.Unlock()

	delta := deltaPool.Get().(*entity.Delta)
	delta.Seq = h.store.Seq()
	delta.Added = delta.Added[:0]
	delta.Updated = delta.Updated[:0]
	delta.Removed = delta.Removed[:0]
	for k := range delta.Clusters {
		delete(delta.Clusters, k)
	}
	delta.Clusters = buf.Clusters

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

	for id := range c.seen {
		if _, ok := visibleSet[id]; !ok {
			delta.Removed = append(delta.Removed, id)
			delete(c.seen, id)
		}
	}

	if len(delta.Added) == 0 && len(delta.Updated) == 0 && len(delta.Removed) == 0 && len(delta.Clusters) == 0 {
		deltaPool.Put(delta)
		queryBufferPool.Put(buf)
		return
	}

	deltaBytes, err := json.Marshal(delta)
	deltaPool.Put(delta)
	queryBufferPool.Put(buf)
	if err != nil {
		return
	}

	select {
	case c.ch <- deltaBytes:
		c.lastPush = time.Now()
	default:
		// Client ch is full; skip this update
	}
}

