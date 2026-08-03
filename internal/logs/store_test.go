package logs

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestNewRequestID(t *testing.T) {
	seen := make(map[string]struct{})
	for range 100 {
		id, err := newRequestID()
		if err != nil {
			t.Fatalf("newRequestID() error: %v", err)
		}
		if len(id) != len("req_")+16 {
			t.Fatalf("id length = %d, want %d (%q)", len(id), len("req_")+16, id)
		}
		if id[:4] != "req_" {
			t.Fatalf("id = %q, want req_ prefix", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate generated id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestStoreAppendListAndRingEviction(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{ID: "req_one", Source: SourceLocal, Route: "myapp"})
	if err != nil {
		t.Fatalf("Append() first entry: %v", err)
	}
	_, err = store.Append(Entry{ID: "req_two", Source: SourcePublic, Route: "api.myapp"})
	if err != nil {
		t.Fatalf("Append() second entry: %v", err)
	}
	for i := range logCapacity - 2 {
		_, err = store.Append(Entry{ID: fmt.Sprintf("req_fill_%d", i), Source: SourcePublic, Route: "other"})
		if err != nil {
			t.Fatalf("Append() fill entry %d: %v", i, err)
		}
	}
	_, err = store.Append(Entry{ID: "req_three", Source: SourceLocal, Route: "myapp"})
	if err != nil {
		t.Fatalf("Append() rollover entry: %v", err)
	}

	if _, ok := store.Get("req_one"); ok {
		t.Fatal("oldest entry remains after ring eviction")
	}

	entries := store.List(ListOptions{})
	if len(entries) != logCapacity {
		t.Fatalf("entries = %d, want %d", len(entries), logCapacity)
	}
	if entries[0].ID != "req_two" || entries[len(entries)-1].ID != "req_three" {
		t.Fatalf("ring endpoints = %q, %q, want req_two, req_three", entries[0].ID, entries[len(entries)-1].ID)
	}

	local := store.List(ListOptions{Route: "myapp", Source: SourceLocal})
	if len(local) != 1 || local[0].ID != "req_three" {
		t.Fatalf("local filter = %#v, want req_three", local)
	}

	limited := store.List(ListOptions{Limit: 1})
	if len(limited) != 1 || limited[0].ID != "req_three" {
		t.Fatalf("limited list = %#v, want req_three", limited)
	}
}

func TestStoreListAndWatch(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{ID: "req_one", Source: SourceLocal, Route: "myapp"})
	if err != nil {
		t.Fatalf("Append() initial entry: %v", err)
	}

	snapshot, changed := store.ListAndWatch(ListOptions{Route: "myapp"})
	if len(snapshot) != 1 || snapshot[0].ID != "req_one" {
		t.Fatalf("snapshot = %#v, want req_one", snapshot)
	}

	_, err = store.Append(Entry{ID: "req_two", Source: SourcePublic, Route: "myapp"})
	if err != nil {
		t.Fatalf("Append() later entry: %v", err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("watch did not observe appended entry")
	}

	entries, _ := store.ListAndWatch(ListOptions{Route: "myapp"})
	if len(entries) != 2 || entries[1].ID != "req_two" {
		t.Fatalf("updated entries = %#v, want req_one then req_two", entries)
	}
}

func TestStoreGetReturnsCaptureCopy(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{
		ID:     "req_copy",
		Source: SourceLocal,
		Route:  "myapp",
		Capture: &Capture{
			Request: CapturedMessage{
				Headers:  http.Header{"X-Test": {"original"}},
				Body:     []byte("body"),
				Complete: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	got, ok := store.Get("req_copy")
	if !ok {
		t.Fatal("Get() found no entry")
	}
	got.Capture.Request.Headers.Set("X-Test", "changed")
	got.Capture.Request.Body[0] = 'B'

	again, ok := store.Get("req_copy")
	if !ok {
		t.Fatal("Get() found no entry on second call")
	}
	if value := again.Capture.Request.Headers.Get("X-Test"); value != "original" {
		t.Fatalf("stored header = %q, want original", value)
	}
	if got := string(again.Capture.Request.Body); got != "body" {
		t.Fatalf("stored body = %q, want body", got)
	}
}

func TestStoreListOmitsCaptureData(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{
		ID:     "req_captured",
		Source: SourcePublic,
		Route:  "webhook",
		Capture: &Capture{
			Request: CapturedMessage{Body: []byte("request"), Complete: true},
		},
	})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	entries := store.List(ListOptions{})
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Capture != nil {
		t.Fatalf("list capture = %#v, want nil", entries[0].Capture)
	}

	entry, ok := store.Get("req_captured")
	if !ok {
		t.Fatal("Get() found no entry")
	}
	if entry.Capture == nil {
		t.Fatal("Get() omitted retained capture")
	}
}

func TestStoreRingEvictionRemovesMetadataAndCapture(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{
		ID:      "req_one",
		Source:  SourcePublic,
		Capture: &Capture{Request: CapturedMessage{Body: []byte("request"), Complete: true}},
	})
	if err != nil {
		t.Fatalf("Append() captured entry: %v", err)
	}
	for i := range logCapacity - 1 {
		_, err = store.Append(Entry{ID: fmt.Sprintf("req_fill_%d", i), Source: SourceLocal})
		if err != nil {
			t.Fatalf("Append() fill entry %d: %v", i, err)
		}
	}
	_, err = store.Append(Entry{
		ID:      "req_three",
		Source:  SourcePublic,
		Capture: &Capture{Request: CapturedMessage{Body: []byte("new request"), Complete: true}},
	})
	if err != nil {
		t.Fatalf("Append() rollover capture: %v", err)
	}

	if _, ok := store.Get("req_one"); ok {
		t.Fatal("oldest captured entry remains after ring eviction")
	}
	third, ok := store.Get("req_three")
	if !ok || third.Capture == nil {
		t.Fatalf("third entry = %#v, want captured req_three", third)
	}
}

func TestStoreRejectsCaptureOverMessageLimit(t *testing.T) {
	store := NewStore()
	_, err := store.Append(Entry{
		ID:     "req_too_large",
		Source: SourceLocal,
		Capture: &Capture{
			Request: CapturedMessage{
				Body:     make([]byte, maxCapturedMessageBytes+1),
				Complete: false,
			},
		},
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("Append() error = %v, want ErrInvalidEntry", err)
	}
}
