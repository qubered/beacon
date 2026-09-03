package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T, opts Options) (*Spool, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func result(n int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"slot":%d,"status":"up"}`, n))
}

func capture(size int) json.RawMessage {
	return json.RawMessage(`{"pad":"` + strings.Repeat("x", size) + `"}`)
}

func TestSpool_AddPeekAck(t *testing.T) {
	s, _ := open(t, Options{})

	for i := 0; i < 3; i++ {
		if _, err := s.Add(result(i), nil); err != nil {
			t.Fatal(err)
		}
	}

	items, err := s.Peek(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("peeked %d items, want 3", len(items))
	}
	// Oldest first: a backlog must drain in the order it was collected.
	for i, it := range items {
		if string(it.Result) != string(result(i)) {
			t.Errorf("item %d = %s, want %s", i, it.Result, result(i))
		}
	}

	if err := s.Ack(items[0].Seq, items[1].Seq); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Items; got != 1 {
		t.Fatalf("%d items after acking two of three", got)
	}
}

// TestSpool_PeekIsAtLeastOnce: Peek must not remove. Removing on read is
// at-most-once and loses results whenever the process dies between sending and
// Core committing — a duplicate is harmless because the scheduled slot
// deduplicates on insert, but a lost result is a permanent hole in uptime.
func TestSpool_PeekIsAtLeastOnce(t *testing.T) {
	s, _ := open(t, Options{})
	if _, err := s.Add(result(1), nil); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		items, err := s.Peek(10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("peek %d returned %d items; an unacknowledged item must stay", i, len(items))
		}
	}

	s.Ack(1 << 63) // acking an unknown seq must not disturb anything
	if got := s.Stats().Items; got != 1 {
		t.Fatalf("%d items after acking an unknown seq", got)
	}
}

// TestSpool_ShedsCapturesBeforeResults is invariant I6, and the whole reason
// captures and results are separate files.
//
// A capture is the large payload and the least load-bearing; status rows are
// what uptime and history are computed from. Dropping a result to save a
// capture trades a permanent hole in history for a diagnostic nobody asked for.
func TestSpool_ShedsCapturesBeforeResults(t *testing.T) {
	// Room for the results but nowhere near enough for the captures.
	s, _ := open(t, Options{MaxBytes: 2000})

	for i := 0; i < 10; i++ {
		if _, err := s.Add(result(i), capture(500)); err != nil {
			t.Fatal(err)
		}
	}

	st := s.Stats()
	if st.DroppedResults != 0 {
		t.Fatalf("%d results were dropped while captures remained; captures must go first (I6)", st.DroppedResults)
	}
	if st.Items != 10 {
		t.Fatalf("%d results survived, want all 10", st.Items)
	}
	if st.DroppedCaptures == 0 {
		t.Fatal("nothing was shed despite exceeding the size bound")
	}

	// Every surviving result must still be readable and complete.
	items, err := s.Peek(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatalf("peeked %d, want 10", len(items))
	}
	for _, it := range items {
		var v map[string]any
		if err := json.Unmarshal(it.Result, &v); err != nil {
			t.Fatalf("a surviving result is not valid JSON: %v", err)
		}
	}
}

// TestSpool_ShedsResultsOnlyWhenNoCapturesRemain, and then oldest first: the
// newest result is the one most likely to still describe the device's current
// state.
func TestSpool_ShedsResultsOnlyWhenNoCapturesRemain(t *testing.T) {
	s, _ := open(t, Options{MaxBytes: 200})

	for i := 0; i < 20; i++ {
		if _, err := s.Add(result(i), capture(100)); err != nil {
			t.Fatal(err)
		}
	}

	st := s.Stats()
	if st.DroppedResults == 0 {
		t.Fatal("nothing was shed despite a bound far below the payload")
	}

	items, err := s.Peek(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if len(it.Capture) != 0 {
			t.Error("a capture survived while results were being dropped; the order is wrong")
		}
	}
	// What survived must be the newest.
	if len(items) > 0 {
		var v struct {
			Slot int `json:"slot"`
		}
		json.Unmarshal(items[len(items)-1].Result, &v)
		if v.Slot != 19 {
			t.Errorf("the newest surviving result is slot %d, want 19 — results shed oldest-first", v.Slot)
		}
	}
}

// TestSpool_BothDropClassesAreCounted: a non-zero DroppedResults means history
// has a hole, which is a different conversation from a non-zero
// DroppedCaptures. Conflating them hides the one that matters.
func TestSpool_BothDropClassesAreCounted(t *testing.T) {
	s, _ := open(t, Options{MaxBytes: 150})
	for i := 0; i < 12; i++ {
		s.Add(result(i), capture(80))
	}
	st := s.Stats()
	if st.DroppedCaptures == 0 || st.DroppedResults == 0 {
		t.Fatalf("drop counters are captures=%d results=%d; both classes must be counted separately",
			st.DroppedCaptures, st.DroppedResults)
	}
}

// TestSpool_AgeBoundExpiresIdleItems: size alone lets a quiet agent hold a
// week-old result nobody wants.
func TestSpool_AgeBoundExpiresIdleItems(t *testing.T) {
	s, dir := open(t, Options{MaxAge: 50 * time.Millisecond})
	if _, err := s.Add(result(1), nil); err != nil {
		t.Fatal(err)
	}
	if s.Stats().Items != 1 {
		t.Fatal("the item was not stored")
	}

	time.Sleep(80 * time.Millisecond)
	s.Prune()

	if got := s.Stats().Items; got != 0 {
		t.Fatalf("%d items survived past the age bound", got)
	}
	if s.Stats().DroppedResults != 1 {
		t.Errorf("the expiry was not counted as a dropped result")
	}
	// The files must actually be gone, not merely forgotten.
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if strings.Contains(f.Name(), "result.json") {
			t.Errorf("%s survived on disk after expiry", f.Name())
		}
	}
}

// TestSpool_SurvivesRestart is the entire reason the spool is on disk: an agent
// that restarts mid-outage must still hold what it collected, or the gap it was
// buffering against becomes a real hole in history.
func TestSpool_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.Add(result(i), capture(20)); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := s.Peek(100)

	// The agent restarts.
	reopened, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	after, err := reopened.Peek(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(first) {
		t.Fatalf("%d items survived a restart, want %d", len(after), len(first))
	}
	for i := range after {
		if string(after[i].Result) != string(first[i].Result) {
			t.Errorf("item %d changed across the restart", i)
		}
		if len(after[i].Capture) == 0 {
			t.Errorf("item %d lost its capture across the restart", i)
		}
	}

	// A new item must not reuse a sequence number, or an Ack for the old item
	// would delete the new one.
	seq, err := reopened.Add(result(99), nil)
	if err != nil {
		t.Fatal(err)
	}
	if seq < after[len(after)-1].Seq {
		t.Fatalf("new seq %d collides with recovered seq %d", seq, after[len(after)-1].Seq)
	}
}

// TestSpool_OrphanedCaptureIsDiscarded: the process died between writing the
// result and the capture. A capture nothing can be attributed to is unreadable.
func TestSpool_OrphanedCaptureIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, fmt.Sprintf("%020d.capture.json", 7))
	if err := os.WriteFile(orphan, capture(10), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Items; got != 0 {
		t.Fatalf("%d items recovered from an orphaned capture", got)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("the orphaned capture file was left on disk")
	}
}

// TestSpool_PartialWriteIsNotRecovered: a half-written result would be
// unparseable JSON handed to the link on every retry forever, so writes are
// atomic via a temp file and rename.
func TestSpool_PartialWriteIsNotRecovered(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, fmt.Sprintf("%020d.result.json.tmp", 3))
	if err := os.WriteFile(tmp, []byte(`{"slot":3,"stat`), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Items; got != 0 {
		t.Fatalf("%d items recovered; a .tmp file is a torn write and must not be adopted", got)
	}
}

func TestSpool_RejectsAnEmptyResult(t *testing.T) {
	s, _ := open(t, Options{})
	if _, err := s.Add(nil, capture(10)); err == nil {
		t.Fatal("an item with no result was accepted")
	}
}

func TestSpool_ConcurrentUseIsSafe(t *testing.T) {
	s, _ := open(t, Options{MaxBytes: 4096})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Add(result(i*100+j), capture(32))
				s.Peek(5)
				s.Stats()
				s.Prune()
			}
		}(i)
	}
	wg.Wait()
}
