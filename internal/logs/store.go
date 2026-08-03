// Store retains the agent's recent requests in one in-memory ring.
//
// The agent has no request-log database. Store owns the last 1,024 requests
// from both local and public traffic. Each entry may include Capture data when
// the route opted in. When the ring fills, adding a new entry removes the
// oldest entry and its capture together.
//
//	proxy completes request
//	  -> Store.Append
//	  -> remove oldest entry if the ring is full
//	  -> append the new entry and index it by request ID
//
// `routeup logs` will call List, which returns metadata only so normal log
// output never exposes captured headers or bodies. Future inspect and replay
// commands will call Get, which returns a deep copy of the complete entry.
//
// Store is shared by concurrent proxy requests. Public methods lock Store.mu.
// Helpers ending in Locked must be called only while that mutex is held; they
// do not lock themselves so one operation can update the ring and ID index as
// one atomic change without deadlocking.
package logs

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

const logCapacity = 1_024

var (
	// ErrInvalidEntry means an entry cannot be added to the store.
	ErrInvalidEntry = errors.New("invalid log entry")

	// ErrDuplicateID means an entry ID is already present in the ring.
	ErrDuplicateID = errors.New("duplicate request id")
)

// ListOptions restricts entries returned by List. An empty Route or Source
// matches all values. Limit returns the newest matching entries while
// preserving chronological order.
type ListOptions struct {
	Route  string
	Source Source
	Limit  int
}

// Store holds bounded chronological request entries. It is safe for concurrent use
type Store struct {
	mu sync.Mutex

	entries []Entry
	start   int
	size    int
	byID    map[string]int
}

// NewStore returns a store with routeup's fixed in-memory entry limit.
func NewStore() *Store {
	return &Store{
		entries: make([]Entry, logCapacity),
		byID:    make(map[string]int, logCapacity),
	}
}

// Append stores one completed entry and returns its assigned ID. A blank ID is
// generated automatically. When entry has Capture, Append transfers ownership
// of its retained byte slices to the store; callers must not mutate them after
// this call.
func (store *Store) Append(entry Entry) (Entry, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if !entry.Source.valid() {
		return Entry{}, fmt.Errorf("source %q: %w", entry.Source, ErrInvalidEntry)
	}
	if entry.StartedAt.IsZero() {
		entry.StartedAt = time.Now()
	}
	if entry.ID == "" {
		id, err := store.generateIDLocked()
		if err != nil {
			return Entry{}, err
		}
		entry.ID = id
	}
	if _, exists := store.byID[entry.ID]; exists {
		return Entry{}, fmt.Errorf("%s: %w", entry.ID, ErrDuplicateID)
	}
	if !entry.Capture.withinLimit() {
		return Entry{}, fmt.Errorf("capture exceeds the message limit: %w", ErrInvalidEntry)
	}

	if store.size == len(store.entries) {
		store.removeOldestLocked()
	}
	index := (store.start + store.size) % len(store.entries)
	store.entries[index] = entry
	store.byID[entry.ID] = index
	store.size++

	return metadataEntry(entry), nil
}

// List returns matching entries from oldest to newest without capture data.
func (store *Store) List(opts ListOptions) []Entry {
	store.mu.Lock()
	defer store.mu.Unlock()

	entries := store.matchingEntriesLocked(opts)
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[len(entries)-opts.Limit:]
	}
	return entries
}

// Get returns a deep copy of one complete entry by ID.
func (store *Store) Get(id string) (Entry, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	index, ok := store.byID[id]
	if !ok {
		return Entry{}, false
	}
	return cloneEntry(store.entries[index]), true
}

func (opts ListOptions) matches(entry Entry) bool {
	if opts.Route != "" && opts.Route != entry.Route {
		return false
	}
	if opts.Source != "" && opts.Source != entry.Source {
		return false
	}
	return true
}

func (store *Store) generateIDLocked() (string, error) {
	const attempts = 4
	for range attempts {
		id, err := newRequestID()
		if err != nil {
			return "", fmt.Errorf("generate request id: %w", err)
		}
		if _, exists := store.byID[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique request id: %w", ErrDuplicateID)
}

func (store *Store) matchingEntriesLocked(opts ListOptions) []Entry {
	entries := make([]Entry, 0, store.size)
	for offset := range store.size {
		index := (store.start + offset) % len(store.entries)
		entry := store.entries[index]
		if opts.matches(entry) {
			entries = append(entries, metadataEntry(entry))
		}
	}
	return entries
}

func (store *Store) removeOldestLocked() {
	oldest := store.entries[store.start]
	delete(store.byID, oldest.ID)
	store.entries[store.start] = Entry{}
	store.start = (store.start + 1) % len(store.entries)
	store.size--
}

func cloneEntry(entry Entry) Entry {
	out := entry
	out.Capture = entry.Capture.clone()
	return out
}

func metadataEntry(entry Entry) Entry {
	out := entry
	out.Capture = nil
	return out
}

func newRequestID() (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "req_" + base64.RawURLEncoding.EncodeToString(random[:]), nil
}
