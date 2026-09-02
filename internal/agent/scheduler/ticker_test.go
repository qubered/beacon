package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestTicker_FiresOnlyWhenTheSlotArrives(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	tk.Set("m1", Schedule{Interval: minute}, start)

	when, ok := tk.NextWake()
	if !ok {
		t.Fatal("NextWake reported nothing scheduled")
	}
	if due := tk.Due(when.Add(-time.Second)); len(due) != 0 {
		t.Fatalf("a monitor fired %v before its slot", due)
	}
	due := tk.Due(when)
	if len(due) != 1 || due[0].MonitorID != "m1" {
		t.Fatalf("due = %v, want exactly m1", due)
	}
	if !due[0].Slot.Equal(when) {
		t.Errorf("slot = %s, want the scheduled %s — the slot travels to storage as the execution fence", due[0].Slot, when)
	}
}

// TestTicker_ConfigChangeResetsAndSuppressesTheMissedCount: re-enabling a
// monitor that was disabled for a week must not add six figures to a counter
// that alerts (spec §8).
func TestTicker_ConfigChangeResetsAndSuppressesTheMissedCount(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	tk.Set("m1", Schedule{Interval: minute}, start)

	// A week passes with the agent not running this monitor.
	aWeekLater := start.Add(7 * 24 * time.Hour)

	// The operator re-enables it, which re-sets the schedule.
	tk.Set("m1", Schedule{Interval: minute}, aWeekLater)

	due := tk.Due(aWeekLater.Add(2 * minute))
	if len(due) != 1 {
		t.Fatalf("due = %v, want one run", due)
	}
	if due[0].Missed != 0 {
		t.Fatalf("missed = %d after a schedule reset, want 0 — a week of absence must not land on the counter", due[0].Missed)
	}

	// The suppression is for exactly one tick; a genuine gap after that still
	// counts, or the counter would be permanently blind.
	slot := due[0].Slot
	due = tk.Due(slot.Add(10 * minute))
	if len(due) != 1 || due[0].Missed == 0 {
		t.Fatalf("a real gap after the reset reported %v; suppression must last one tick only", due)
	}
}

// TestTicker_MissedRunsAreReportedNotReplayed: a ten-minute stop yields one
// run and a gap count, not ten queued runs.
func TestTicker_MissedRunsAreReportedNotReplayed(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	tk.Set("m1", Schedule{Interval: minute}, start)

	// Consume the first tick so the reset suppression is spent.
	first := tk.Due(start.Add(minute))
	if len(first) != 1 {
		t.Fatalf("expected one initial run, got %v", first)
	}

	// The agent stops for ten minutes after that first run. Ten slots come due
	// in the meantime: nine are skipped and the tenth is the current one,
	// which runs.
	after := first[0].Slot.Add(10 * minute)
	due := tk.Due(after)

	if len(due) != 1 {
		t.Fatalf("a ten-minute stop produced %d runs; it must produce exactly one plus a recorded gap", len(due))
	}
	// Assert the accounting rather than a bare number: every slot that came
	// due is either run or recorded as missed. Losing one silently is the bug
	// that makes a gap invisible.
	const slotsElapsed = 10
	if accounted := due[0].Missed + len(due); accounted != slotsElapsed {
		t.Errorf("%d slots came due but %d were accounted for (%d missed + %d run)",
			slotsElapsed, accounted, due[0].Missed, len(due))
	}
	// The run must carry the *current* slot. A run executing now but stamped
	// ten minutes ago lands in history at a time nothing was observed.
	if due[0].Slot.Before(after.Add(-minute)) {
		t.Errorf("run slot %s is more than an interval before now %s — that is a backdated run", due[0].Slot, after)
	}
}

// TestTicker_SuspectResumeReAlignsToTheGrid: a monitor that was briefly
// suspect must return to the fleet-wide grid, or the spread that keeps four
// hundred monitors from bunching decays with every incident.
func TestTicker_SuspectResumeReAlignsToTheGrid(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	tk.Set("m1", Schedule{Interval: minute, SuspectInterval: 10 * time.Second}, start)

	tk.SetSuspect("m1", true, start)
	due := tk.Due(start.Add(2 * minute))
	if len(due) != 1 {
		t.Fatalf("due = %v", due)
	}

	// Recovered, at an awkward moment mid-slot.
	recoveredAt := due[0].Slot.Add(3500 * time.Millisecond)
	tk.SetSuspect("m1", false, recoveredAt)

	when, _ := tk.NextWake()
	phase := Phase("m1", minute)
	if offset := time.Duration(when.UnixNano()) % minute; offset != phase {
		t.Fatalf("after recovery the monitor sits at offset %s, want the grid phase %s", offset, phase)
	}
}

// TestTicker_FourHundredMonitorsDoNotBunch is the exit gate measured through
// the Ticker rather than through Phase alone: across one minute of ticking, no
// single second may carry the fleet.
func TestTicker_FourHundredMonitorsDoNotBunch(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	for i := 0; i < 400; i++ {
		tk.Set(fmt.Sprintf("11111111-2222-3333-4444-%012d", i), Schedule{Interval: minute}, start)
	}

	// Drain the first (suppressed) tick, then measure a clean minute.
	tk.Due(start.Add(minute))

	perSecond := make([]int, 60)
	for sec := 0; sec < 60; sec++ {
		now := start.Add(minute + time.Duration(sec+1)*time.Second)
		perSecond[sec] = len(tk.Due(now))
	}

	total, worst := 0, 0
	for _, n := range perSecond {
		total += n
		if n > worst {
			worst = n
		}
	}
	if total != 400 {
		t.Fatalf("%d of 400 monitors fired across the minute", total)
	}
	if worst > 20 {
		t.Errorf("the busiest second ran %d of 400 monitors; they are bunching", worst)
	}
}

func TestTicker_RemoveStopsAMonitor(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	tk.Set("m1", Schedule{Interval: minute}, start)
	tk.Remove("m1")

	if n := tk.Len(); n != 0 {
		t.Fatalf("Len = %d after Remove", n)
	}
	if due := tk.Due(start.Add(time.Hour)); len(due) != 0 {
		t.Fatalf("a removed monitor still fired: %v", due)
	}
	if _, ok := tk.NextWake(); ok {
		t.Error("NextWake reported work with no monitors scheduled")
	}
}

// TestTicker_ConcurrentUseIsSafe: the agent will reconfigure monitors from the
// link goroutine while the run loop is calling Due.
func TestTicker_ConcurrentUseIsSafe(t *testing.T) {
	tk := NewTicker()
	start := at(0)
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m%d", i)
			for j := 0; j < 200; j++ {
				tk.Set(id, Schedule{Interval: minute}, start)
				tk.SetSuspect(id, j%2 == 0, start)
				tk.Due(start.Add(time.Duration(j) * time.Second))
				tk.NextWake()
				tk.Len()
			}
			tk.Remove(id)
		}(i)
	}
	wg.Wait()
}
