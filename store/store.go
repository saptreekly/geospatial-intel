package store

import (
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/saptreekly/geospatial-intel/entity"
	"github.com/saptreekly/geospatial-intel/spatial"
	"github.com/saptreekly/geospatial-intel/util"
)

type StoreEvent struct {
	Seq     uint64
	Changed []string
	Removed []string
}

type Store struct {
	mu        sync.Mutex
	db        *sql.DB
	index     *spatial.Index
	seq       atomic.Uint64
	lastSeen  map[string]uint64
	subs      map[chan StoreEvent]struct{}
	historyChan chan []entity.Entity
}

func (s *Store) Apply(entities []entity.Entity) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "Store.Apply")

	s.mu.Lock()
	seq := s.seq.Add(1)

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

	removed := []string{}
	for id, lastSeq := range s.lastSeen {
		if _, present := seenIDs[id]; !present {
			if lastSeq < seq-1 {
				removed = append(removed, id)
				delete(s.lastSeen, id)
			} else {
				s.lastSeen[id] = seq - 2
			}
		}
	}
	s.mu.Unlock()

	// Pass cleanly to the spatial index package boundary
	s.index.BatchUpdateRust(append(added, updated...), removed)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.historyChan <- append(added, updated...)

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

	for ch := range s.subs {
		select {
		case ch <- event:
		default:
		}
	}
}
