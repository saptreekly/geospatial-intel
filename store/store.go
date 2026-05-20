package store

import (
	"database/sql"
	"log"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/spatial"
	"github.com/saptreekly/geospatial-intel/util"
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
	db    *sql.DB

	mu          sync.Mutex
	seq         atomic.Uint64
	lastSeen    map[string]uint64       // entity ID → seq of last poll
	subs        map[int]chan StoreEvent // subscriber ID → channel
	nextSubID   int
	historyChan chan []entity.Entity
}

// NewStore creates a new entity store.
func NewStore() *Store {
	db, err := sql.Open("sqlite3", "osint.db")
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS history (
		entity_id TEXT,
		timestamp INTEGER,
		lat REAL,
		lng REAL,
		PRIMARY KEY (entity_id, timestamp)
	);
	CREATE INDEX IF NOT EXISTS idx_history_entity_id_timestamp ON history (entity_id, timestamp DESC);`)
	if err != nil {
		panic(err)
	}

	s := &Store{
		index:       spatial.NewIndex(),
		db:          db,
		lastSeen:    make(map[string]uint64),
		subs:        make(map[int]chan StoreEvent),
		historyChan: make(chan []entity.Entity, 100),
	}
	go s.historyWorker()
	return s
}

// historyWorker processes historical entity writes sequentially.
func (s *Store) historyWorker() {
	for entities := range s.historyChan {
		s.recordHistory(entities)
	}
}

// recordHistory writes entity coordinates to the SQLite store.
func (s *Store) recordHistory(entities []entity.Entity) {
	start := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare("INSERT INTO history (entity_id, timestamp, lat, lng) VALUES (?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return
	}
	defer stmt.Close()

	for _, e := range entities {
		stmt.Exec(e.ID, e.UpdatedAt, e.Lat, e.Lng)
	}
	tx.Commit()

	if time.Since(start) > 1*time.Millisecond {
		msg := "CRITICAL: SQLite disk transaction slow: " + time.Since(start).String()
		log.Printf("%s", msg)
		util.LogPerformance(msg)
	}
}

// Apply ingests fresh entities from a seeder.
// Diffs against current state, emits StoreEvent to all subscribers.
func (s *Store) Apply(entities []entity.Entity) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "Store.Apply")

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
	s.index.BatchUpdateRust(append(added, updated...), removed)

	// Record history
	s.historyChan <- append(added, updated...)

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

// GetHistory returns the last 100 coordinates for an entity.
func (s *Store) GetHistory(id string) ([]entity.Entity, error) {
	rows, err := s.db.Query("SELECT timestamp, lat, lng FROM history WHERE entity_id = ? ORDER BY timestamp DESC LIMIT 100", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []entity.Entity
	for rows.Next() {
		var e entity.Entity
		e.ID = id
		err := rows.Scan(&e.UpdatedAt, &e.Lat, &e.Lng)
		if err != nil {
			return nil, err
		}
		history = append(history, e)
	}
	return history, nil
}
