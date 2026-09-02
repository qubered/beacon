package scheduler

import (
	"sort"
	"sync"
	"time"
)

// Due is one monitor that has come up for execution.
type Due struct {
	MonitorID string

	// Slot is the scheduled time this run belongs to, not the time it was
	// dequeued. It travels with the result all the way to storage, where it is
	// the unique execution fence that makes an at-least-once spool replay
	// deduplicate on insert rather than double-count (spec §7.3).
	Slot time.Time

	// Missed is how many slots were skipped to get here, from a snap forward
	// after the agent was stopped or overloaded. The caller adds it to the
	// monitor's missed-runs counter; it is not a failure and produces no run
	// rows, because a recorded gap is worth more than invented data.
	Missed int
}

// Ticker holds the live schedule for every monitor assigned to this agent.
//
// It owns *when*, and nothing else — it does not execute, store or interpret
// anything. All of its scheduling decisions come from Schedule's pure
// arithmetic; what it adds is the set of monitors, their next slots, and the
// bookkeeping around configuration changes.
type Ticker struct {
	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	schedule Schedule
	nextSlot time.Time
	suspect  bool

	// suppressMissed skips the missed-run increment for exactly one tick after
	// a schedule reset. Re-enabling a monitor that was disabled for a week
	// would otherwise add six figures to a counter that alerts, which teaches
	// people to ignore that counter (spec §8).
	suppressMissed bool
}

func NewTicker() *Ticker {
	return &Ticker{entries: map[string]*entry{}}
}

// Set adds a monitor or replaces its schedule, resetting it onto the grid.
//
// Calling this for an unchanged schedule would reset a monitor that did not
// need resetting, so callers should compare first; Ticker cannot tell an
// edited monitor from a re-sent identical one, and guessing wrong in this
// direction silently skips a run.
func (t *Ticker) Set(monitorID string, s Schedule, now time.Time) {
	s.MonitorID = monitorID
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[monitorID] = &entry{
		schedule:       s,
		nextSlot:       s.First(now),
		suppressMissed: true,
	}
}

// Remove drops a monitor, for unassignment or deletion.
func (t *Ticker) Remove(monitorID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, monitorID)
}

// SetSuspect switches a monitor between its normal and faster cadence.
//
// Leaving the suspect state re-aligns to the shared grid rather than carrying
// the faster cadence's phase forward, so a monitor that was briefly suspect
// does not keep an off-grid phase forever.
func (t *Ticker) SetSuspect(monitorID string, suspect bool, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[monitorID]
	if !ok || e.suspect == suspect {
		return
	}
	e.suspect = suspect
	if !suspect {
		e.nextSlot = e.schedule.Resume(now)
	}
}

// Due returns every monitor whose slot has arrived, advancing each one past
// it, and reports how many slots each skipped.
//
// Results are ordered by slot so a backlog drains oldest-first, which keeps
// the order runs are executed in matching the order they were scheduled in.
func (t *Ticker) Due(now time.Time) []Due {
	t.mu.Lock()
	defer t.mu.Unlock()

	var due []Due
	for id, e := range t.entries {
		if e.nextSlot.After(now) {
			continue
		}
		slot, missed, next := e.schedule.Catchup(e.nextSlot, now, e.suspect)
		if e.suppressMissed {
			missed = 0
			e.suppressMissed = false
		}
		e.nextSlot = next
		due = append(due, Due{MonitorID: id, Slot: slot, Missed: missed})
	}

	sort.Slice(due, func(i, j int) bool {
		if due[i].Slot.Equal(due[j].Slot) {
			return due[i].MonitorID < due[j].MonitorID
		}
		return due[i].Slot.Before(due[j].Slot)
	})
	return due
}

// NextWake is when Due should next be called: the earliest pending slot.
//
// A caller sleeping until this rather than polling on a fixed tick is what
// keeps a thousand idle monitors from waking the process every second. ok is
// false when no monitors are scheduled.
func (t *Ticker) NextWake() (when time.Time, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.entries {
		if !ok || e.nextSlot.Before(when) {
			when, ok = e.nextSlot, true
		}
	}
	return when, ok
}

// Len is the number of scheduled monitors.
func (t *Ticker) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}
