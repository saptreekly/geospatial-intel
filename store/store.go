package store

import (
	"sync"
	"sync/atomic"

	"github.com/jackweekly/geospatial-server/entity"
	"github.com/jackweekly/geospatial-server/spatial"
)

// StoreEvent is emitted when entities change.
type StoreEvent struct {
	Seq     uint64
	Changed []string // IDs of added or updated entities
	Removed []string
}

// Store holds all entities, tracks changes, and manages subscriptions.
type Store struct {
	index *spatial.Index

	mu       sync.Mutex
	seq      atomic.Uint64
	lastSeen map[string]uint64              // entity ID → seq of last poll
	subs     map[int]chan StoreEvent        // subscriber ID → channel
	nextSubID int
}

// NewStore creates a new entity store.
func NewStore() *Store {
	return &Store{
		index:    spatial.NewIndex(),
		lastSeen: make(map[string]uint64),
		subs:     make(map[int]chan StoreEvent),
	}
}

// Apply ingests fresh entities from a seeder.
// Diffs against current state, emits StoreEvent to all subscribers.
func (s *Store) Apply(entities []entity.Entity) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.seq.Add(1)

	// Assign versions and determine added/updated
	added := []entity.Entity{}
	updated := []entity.Entity{}
	seenIDs := make(map[string]struct{})

	for _, e := range entities {
		e.Version = seq
		seenIDs[e.ID] = struct{}{}

		if _, seen := s.lastSeen[e.ID]; !seen {
			added = append(added, e)
		} else {
			updated = append(updated, e)
		}
		s.lastSeen[e.ID] = seq
	}

	// Find removed entities (not seen in this poll or previous one)
	removed := []string{}
	for id, lastSeq := range s.lastSeen {
		if _, present := seenIDs[id]; !present {
			// Entity was in previous poll but not this one
			// If it was also absent 2 polls ago, mark as removed
			if lastSeq < seq-1 {
				removed = append(removed, id)
				delete(s.lastSeen, id)
			} else {
				// Mark as stale for next poll
				s.lastSeen[id] = seq - 2
			}
		}
	}

	// Update spatial index
	s.index.Update(append(added, updated...), removed)

	// Emit event to all subscribers
	event := StoreEvent{
		Seq:     seq,
		Changed: make([]string, 0, len(added)+len(updated)),
		Removed: removed,
	}
	for _, e := range added {
		event.Changed = append(event.Changed, e.ID)
	}
	for _, e := range updated {
		event.Changed = append(event.Changed, e.ID)
	}

	for _, ch := range s.subs {
		select {
		case ch <- event:
		default:
			// Non-blocking send; drop if subscriber is slow
		}
	}
}

// Subscription wraps a channel with its ID for later unsubscription.
type Subscription struct {
	ch chan StoreEvent
	id int
}

// C returns the receive-only channel for this subscription.
func (sub *Subscription) C() <-chan StoreEvent {
	return sub.ch
}

// Subscribe returns a Subscription that receives StoreEvents.
func (s *Store) Subscribe() *Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan StoreEvent, 1)
	id := s.nextSubID
	s.nextSubID++
	s.subs[id] = ch
	return &Subscription{ch: ch, id: id}
}

// Unsubscribe removes a subscription from the subscriber list.
func (s *Store) Unsubscribe(sub *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ch, ok := s.subs[sub.id]; ok {
		close(ch)
		delete(s.subs, sub.id)
	}
}

// Query returns entities visible in the viewport and cluster counts.
func (s *Store) Query(vp entity.Viewport) (visible []entity.Entity, clusters map[string]entity.Cluster, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index.Query(vp)
}
