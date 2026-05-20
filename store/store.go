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

type StoreEvent struct {
	Seq     uint64
	Changed []string
	Removed []string
}

type Subscription struct {
	ch chan StoreEvent
	id int
}

func (sub *Subscription) C() <-chan StoreEvent {
	return sub.ch
}

type Store struct {
	mu          sync.Mutex
	db          *sql.DB
	index       *spatial.Index
	seq         atomic.Uint64
	lastSeen    map[string]uint64
	subs        map[int]chan StoreEvent
	nextSubID   int
	historyChan chan []entity.Entity

	// Persistent scratchpad buffers
	addedBuf    []entity.Entity
	updatedBuf  []entity.Entity
	removedBuf  []string
	seenIDsBuf  map[string]struct{}
}

func NewStore() *Store {
	db, err := sql.Open("sqlite3", "file:osint.db?cache=shared&mode=rwc&_journal=WAL&_sync=0")
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
		addedBuf:    make([]entity.Entity, 0, 5000),
		updatedBuf:  make([]entity.Entity, 0, 5000),
		removedBuf:  make([]string, 0, 5000),
		seenIDsBuf:  make(map[string]struct{}, 5000),
	}
	go s.historyWorker()
	return s
}

func (s *Store) historyWorker() {
	for entities := range s.historyChan {
		s.recordHistory(entities)
	}
}

func (s *Store) recordHistory(entities []entity.Entity) {
	start := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return
	}

	const chunkSize = 5000
	for i := 0; i < len(entities); i += chunkSize {
		end := i + chunkSize
		if end > len(entities) {
			end = len(entities)
		}
		chunk := entities[i:end]

		// Construct bulk insert
		numRows := len(chunk)
		query := "INSERT INTO history (entity_id, timestamp, lat, lng) VALUES "
		args := make([]interface{}, 0, numRows*4)
		for j := 0; j < numRows; j++ {
			if j > 0 {
				query += ", "
			}
			query += "(?, ?, ?, ?)"
			args = append(args, chunk[j].ID, chunk[j].UpdatedAt, chunk[j].Lat, chunk[j].Lng)
		}

		_, err := tx.Exec(query, args...)
		if err != nil {
			tx.Rollback()
			return
		}
	}

	tx.Commit()

	if time.Since(start) > 200*time.Millisecond {
		msg := "CRITICAL: SQLite disk transaction slow: " + time.Since(start).String()
		log.Printf("%s", msg)
		util.LogPerformance(msg)
	}
}

func (s *Store) Apply(entities []entity.Entity) {
	start := time.Now()
	defer util.LogIfSlow(start, 50*time.Millisecond, "Store.Apply")

	s.mu.Lock()
	defer s.mu.Unlock() // Hold lock for the whole tick

	seq := s.seq.Add(1)

	// Reset buffers
	s.addedBuf = s.addedBuf[:0]
	s.updatedBuf = s.updatedBuf[:0]
	s.removedBuf = s.removedBuf[:0]
	for k := range s.seenIDsBuf {
		delete(s.seenIDsBuf, k)
	}

	for _, e := range entities {
		e.Version = seq
		s.seenIDsBuf[e.ID] = struct{}{}

		if _, seen := s.lastSeen[e.ID]; !seen {
			s.addedBuf = append(s.addedBuf, e)
		} else {
			s.updatedBuf = append(s.updatedBuf, e)
		}
		s.lastSeen[e.ID] = seq
	}

	for id, lastSeq := range s.lastSeen {
		if _, present := s.seenIDsBuf[id]; !present {
			if lastSeq < seq-1 {
				s.removedBuf = append(s.removedBuf, id)
				delete(s.lastSeen, id)
			} else {
				s.lastSeen[id] = seq - 2
			}
		}
	}

	// Prepare data for background workers
	totalChanges := len(s.addedBuf) + len(s.updatedBuf)
	combined := make([]entity.Entity, totalChanges)
	copy(combined, s.addedBuf)
	copy(combined[len(s.addedBuf):], s.updatedBuf)

	// Pure isolated background FFI computation (requires index lock internally)
	s.index.BatchUpdateRust(combined, s.removedBuf)

	// Background history
	select {
	case s.historyChan <- combined:
	default:
	}

	// Subscriptions
	event := StoreEvent{
		Seq:     seq,
		Changed: make([]string, totalChanges),
		Removed: make([]string, len(s.removedBuf)),
	}
	for i, e := range s.addedBuf { event.Changed[i] = e.ID }
	for i, e := range s.updatedBuf { event.Changed[len(s.addedBuf)+i] = e.ID }
	copy(event.Removed, s.removedBuf)

	for _, ch := range s.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Store) Subscribe() *Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan StoreEvent, 1)
	id := s.nextSubID
	s.nextSubID++
	s.subs[id] = ch
	return &Subscription{ch: ch, id: id}
}

func (s *Store) Unsubscribe(sub *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ch, ok := s.subs[sub.id]; ok {
		close(ch)
		delete(s.subs, sub.id)
	}
}

func (s *Store) Query(vp entity.Viewport) (visible []entity.Entity, clusters map[string]entity.Cluster, err error) {
	return s.index.Query(vp)
}

func (s *Store) Seq() uint64 {
	return s.seq.Load()
}


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
