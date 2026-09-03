package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Item is one spooled result, plus its optional capture.
//
// The two are stored as separate files on purpose. Shedding must be able to
// drop the capture while keeping the result (invariant I6), and that is a file
// deletion rather than a rewrite of a record that already reached the disk —
// rewriting under memory pressure is exactly when a rewrite fails.
type Item struct {
	Seq uint64

	// Result is the small, load-bearing part: status, outcome, timing, the
	// scheduled slot. Uptime and history are computed from these.
	Result json.RawMessage

	// Capture is the large, least load-bearing part: per-node input and
	// output. Valuable for diagnosis, never for correctness.
	Capture json.RawMessage
}

// Stats is what the agent exports about its own spool (spec §12 tier 1). Depth
// and both drop counters matter: a non-zero DroppedResults means history has a
// hole, which is a different conversation from a non-zero DroppedCaptures.
type Stats struct {
	Items           int
	Bytes           int64
	DroppedCaptures int
	DroppedResults  int
	OldestAt        time.Time
}

// Options bound the spool. Both bounds are enforced, and both matter: size
// alone lets a quiet agent hold a week-old result nobody wants, and age alone
// lets a busy one fill the disk.
type Options struct {
	MaxBytes int64
	MaxAge   time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxBytes <= 0 {
		o.MaxBytes = 64 << 20
	}
	if o.MaxAge <= 0 {
		o.MaxAge = 24 * time.Hour
	}
	return o
}

// Spool is a durable, bounded, at-least-once outbound queue.
//
// Execution and persistence are decoupled (principle 8): the executor writes
// here and a writer drains it, so monitoring never stops because storage is
// unhappy or the link is down.
type Spool struct {
	dir  string
	opts Options

	mu       sync.Mutex
	nextSeq  uint64
	items    map[uint64]*entry
	dropped  Stats
	totalLen int64
}

type entry struct {
	seq         uint64
	at          time.Time
	resultBytes int64
	captureSize int64
	hasCapture  bool
}

func (e *entry) size() int64 { return e.resultBytes + e.captureSize }

// Open creates or reopens a spool directory, recovering whatever survived a
// restart.
//
// Recovery is the entire point of the spool being on disk: an agent that
// restarts mid-outage must still have the results it collected, or the gap it
// was buffering against becomes a real hole in history.
func Open(dir string, opts Options) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the spool directory: %w", err)
	}
	s := &Spool{dir: dir, opts: opts.withDefaults(), items: map[uint64]*entry{}}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Spool) recover() error {
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("reading the spool directory: %w", err)
	}
	for _, f := range files {
		name := f.Name()
		seqStr, kind, ok := strings.Cut(name, ".")
		if !ok {
			continue
		}
		seq, err := strconv.ParseUint(seqStr, 10, 64)
		if err != nil {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}

		e := s.items[seq]
		if e == nil {
			e = &entry{seq: seq, at: info.ModTime()}
			s.items[seq] = e
		}
		switch kind {
		case "result.json":
			e.resultBytes = info.Size()
			if info.ModTime().Before(e.at) {
				e.at = info.ModTime()
			}
		case "capture.json":
			e.hasCapture = true
			e.captureSize = info.Size()
		}
		if seq >= s.nextSeq {
			s.nextSeq = seq + 1
		}
	}

	// A capture with no result is orphaned — the process died between the two
	// writes. Drop it: a capture nothing can be attributed to is unreadable.
	for seq, e := range s.items {
		if e.resultBytes == 0 {
			s.removeFiles(seq)
			delete(s.items, seq)
			continue
		}
		s.totalLen += e.size()
	}
	return nil
}

// Add writes a result durably and then enforces the bounds.
//
// The result is written before the capture, and the bounds are enforced after
// both. That ordering is what makes the orphan rule above safe: a crash
// between the writes leaves a capture-less result, which is exactly what
// shedding would have produced anyway.
func (s *Spool) Add(result, capture json.RawMessage) (uint64, error) {
	if len(result) == 0 {
		return 0, fmt.Errorf("a spooled item must carry a result")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.nextSeq
	s.nextSeq++
	e := &entry{seq: seq, at: time.Now()}

	if err := s.write(seq, "result.json", result); err != nil {
		return 0, err
	}
	e.resultBytes = int64(len(result))

	if len(capture) > 0 {
		if err := s.write(seq, "capture.json", capture); err != nil {
			// The result is already durable; losing the capture here is the
			// same outcome as shedding it, so the item stays rather than
			// failing the whole write.
			s.dropped.DroppedCaptures++
		} else {
			e.hasCapture = true
			e.captureSize = int64(len(capture))
		}
	}

	s.items[seq] = e
	s.totalLen += e.size()
	s.enforceLocked()
	return seq, nil
}

// Peek returns up to n oldest items without removing them.
//
// Not removing is what makes delivery at-least-once. The alternative —
// removing on read — is at-most-once, and it loses results whenever the
// process dies between sending and Core committing. A duplicate is harmless
// because the scheduled slot is an execution fence and deduplicates on insert
// (spec §7.3); a lost result is a permanent hole in uptime.
func (s *Spool) Peek(n int) ([]Item, error) {
	s.mu.Lock()
	seqs := s.orderedSeqsLocked()
	if n > 0 && len(seqs) > n {
		seqs = seqs[:n]
	}
	s.mu.Unlock()

	out := make([]Item, 0, len(seqs))
	for _, seq := range seqs {
		result, err := os.ReadFile(s.path(seq, "result.json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue // shed between the listing and the read
			}
			return nil, fmt.Errorf("reading spooled result %d: %w", seq, err)
		}
		item := Item{Seq: seq, Result: result}
		if capture, err := os.ReadFile(s.path(seq, "capture.json")); err == nil {
			item.Capture = capture
		}
		out = append(out, item)
	}
	return out, nil
}

// Ack removes items Core has acknowledged. Anything not acknowledged stays and
// is sent again.
func (s *Spool) Ack(seqs ...uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, seq := range seqs {
		e, ok := s.items[seq]
		if !ok {
			continue
		}
		s.totalLen -= e.size()
		delete(s.items, seq)
		s.removeFiles(seq)
	}
	return nil
}

// Prune enforces the age bound. Add enforces it too; this exists so an idle
// agent still expires what it is holding rather than waiting for the next
// result to arrive.
func (s *Spool) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enforceLocked()
}

// enforceLocked applies the age bound, then the size bound.
//
// **Captures are shed before results (invariant I6).** A capture is the large
// payload and the least load-bearing; status and metric rows are what uptime
// and history are computed from. Dropping a result to save a capture would
// trade a permanent hole in history for a diagnostic nobody has asked for yet.
// Only once every capture is gone does the spool start dropping results, and
// then oldest first, because the newest result is the one most likely to still
// describe the device's current state.
func (s *Spool) enforceLocked() {
	// Age first: an expired item should go regardless of how much room there
	// is, or a quiet agent holds week-old results forever.
	if s.opts.MaxAge > 0 {
		cutoff := time.Now().Add(-s.opts.MaxAge)
		for _, seq := range s.orderedSeqsLocked() {
			e := s.items[seq]
			if !e.at.Before(cutoff) {
				break // ordered oldest-first, so nothing later is expired
			}
			s.dropResultLocked(seq)
		}
	}

	if s.totalLen <= s.opts.MaxBytes {
		return
	}

	// Over budget: shed every capture, largest first, before touching a single
	// result. Largest first because the goal is to get under the bound with
	// the fewest diagnostics lost.
	byCaptureSize := s.orderedSeqsLocked()
	sort.SliceStable(byCaptureSize, func(i, j int) bool {
		return s.items[byCaptureSize[i]].captureSize > s.items[byCaptureSize[j]].captureSize
	})
	for _, seq := range byCaptureSize {
		if s.totalLen <= s.opts.MaxBytes {
			return
		}
		if s.items[seq].hasCapture {
			s.dropCaptureLocked(seq)
		}
	}

	// Still over with no captures left. Now results go, oldest first.
	for _, seq := range s.orderedSeqsLocked() {
		if s.totalLen <= s.opts.MaxBytes {
			return
		}
		s.dropResultLocked(seq)
	}
}

func (s *Spool) dropCaptureLocked(seq uint64) {
	e := s.items[seq]
	if e == nil || !e.hasCapture {
		return
	}
	os.Remove(s.path(seq, "capture.json")) //nolint:errcheck // best effort; the accounting is what matters
	s.totalLen -= e.captureSize
	e.hasCapture = false
	e.captureSize = 0
	s.dropped.DroppedCaptures++
}

func (s *Spool) dropResultLocked(seq uint64) {
	e := s.items[seq]
	if e == nil {
		return
	}
	if e.hasCapture {
		// Counted as both: a dropped result takes its capture with it, and a
		// capture counter that ignored this would understate what was lost.
		s.dropped.DroppedCaptures++
	}
	s.totalLen -= e.size()
	delete(s.items, seq)
	s.removeFiles(seq)
	s.dropped.DroppedResults++
}

func (s *Spool) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{
		Items:           len(s.items),
		Bytes:           s.totalLen,
		DroppedCaptures: s.dropped.DroppedCaptures,
		DroppedResults:  s.dropped.DroppedResults,
	}
	for _, seq := range s.orderedSeqsLocked() {
		st.OldestAt = s.items[seq].at
		break
	}
	return st
}

func (s *Spool) orderedSeqsLocked() []uint64 {
	seqs := make([]uint64, 0, len(s.items))
	for seq := range s.items {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

func (s *Spool) path(seq uint64, kind string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%020d.%s", seq, kind))
}

// write persists atomically: a temporary file, fsync, then rename. A partially
// written result recovered after a crash would be unparseable JSON that the
// spool would then hand to the link on every retry forever.
func (s *Spool) write(seq uint64, kind string, data []byte) error {
	final := s.path(seq, kind)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("syncing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	return os.Rename(tmp, final)
}

func (s *Spool) removeFiles(seq uint64) {
	os.Remove(s.path(seq, "result.json"))  //nolint:errcheck
	os.Remove(s.path(seq, "capture.json")) //nolint:errcheck
}
