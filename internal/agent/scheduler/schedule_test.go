package scheduler

import (
	"fmt"
	"testing"
	"time"
)

const minute = time.Minute

func at(sec int) time.Time { return time.Unix(int64(sec), 0).UTC() }

// TestPhase_IsDeterministicAndStable: a restart must land a monitor on the
// same grid it was on before. A phase that changed between processes would
// re-phase the whole fleet on every deploy.
func TestPhase_IsDeterministicAndStable(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := Phase("monitor-abc", minute)
		b := Phase("monitor-abc", minute)
		if a != b {
			t.Fatalf("Phase is not deterministic: %s then %s", a, b)
		}
	}
	if Phase("monitor-abc", minute) == Phase("monitor-abd", minute) {
		t.Error("two different monitor ids produced the same phase; the spread comes from this differing")
	}
	if p := Phase("anything", minute); p < 0 || p >= minute {
		t.Errorf("phase %s is outside [0, interval)", p)
	}
}

// TestPhase_SpreadsFourHundredMonitorsAcrossTheMinute is the roadmap's M3 exit
// gate. Without jitter, four hundred monitors created at deploy time with a
// 60-second interval all fire in the same second and hammer the access switch
// once a minute, forever.
func TestPhase_SpreadsFourHundredMonitorsAcrossTheMinute(t *testing.T) {
	const monitors = 400
	buckets := make([]int, 60) // one per second of the minute

	for i := 0; i < monitors; i++ {
		id := fmt.Sprintf("11111111-2222-3333-4444-%012d", i)
		sec := int(Phase(id, minute) / time.Second)
		buckets[sec]++
	}

	empty := 0
	worst := 0
	for _, n := range buckets {
		if n == 0 {
			empty++
		}
		if n > worst {
			worst = n
		}
	}

	// Uniform would be ~6.7 per second. Assert the shape rather than an exact
	// distribution: no second may carry a large fraction of the fleet, and the
	// minute must not be mostly idle.
	if worst > 20 {
		t.Errorf("the busiest second holds %d of %d monitors; the spread is not working", worst, monitors)
	}
	if empty > 10 {
		t.Errorf("%d of 60 seconds are empty; the spread is clumping", empty)
	}
}

// TestNext_LatenessDoesNotCompound is invariant I10, and the roadmap's exit
// gate: "a monitor that takes 28s to time out still lands on the original
// grid". Advancing from now rather than from the scheduled slot turns a
// 60-second monitor into an 88-second monitor by lunchtime.
func TestNext_LatenessDoesNotCompound(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute}

	slot := at(600)
	for cycle := 0; cycle < 10; cycle++ {
		// Each run overruns by 28 seconds before the next tick is resolved.
		lateBy := slot.Add(28 * time.Second)
		run, missed, next := s.Catchup(slot, lateBy, false)

		if !run.Equal(slot) {
			t.Fatalf("cycle %d: ran at %s, want its scheduled slot %s", cycle, run, slot)
		}
		if missed != 0 {
			t.Fatalf("cycle %d: a run that finished inside its interval reported %d missed", cycle, missed)
		}
		if got := next.Sub(slot); got != minute {
			t.Fatalf("cycle %d: next slot is %s after the last, want exactly %s — lateness compounded", cycle, got, minute)
		}
		slot = next
	}

	if want := at(600).Add(10 * minute); !slot.Equal(want) {
		t.Fatalf("after 10 cycles the grid is at %s, want %s", slot, want)
	}
}

// TestNext_CatchUpSnapsForwardAndRecordsTheGap is the exit gate's third case:
// a ten-minute stop produces one recorded gap, not a queue of backdated runs.
func TestNext_CatchUpSnapsForwardAndRecordsTheGap(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute}

	pending := s.First(at(0))
	now := pending.Add(10 * minute) // the agent was stopped for ten minutes

	run, missed, next := s.Catchup(pending, now, false)

	// The pending slot and the nine after it were all skipped; the tenth
	// interval's slot is the current one and runs now. So a ten-minute stop on
	// a one-minute interval records ten missed runs and performs one — not
	// eleven runs, and not one run with the gap silently unrecorded.
	if missed != 10 {
		t.Errorf("missed = %d, want 10 — one per minute of the outage", missed)
	}
	if !run.Equal(now) {
		t.Errorf("run slot = %s, want the current slot %s", run, now)
	}
	if run.Before(now.Add(-minute)) {
		t.Errorf("run slot %s is more than an interval before now %s — that is a backdated run", run, now)
	}
	if !next.After(now) {
		t.Fatalf("next slot %s is not after now %s", next, now)
	}
	// One more call must not produce a second backlog: the snap is complete.
	if _, missedAgain, _ := s.Catchup(next, next.Add(time.Second), false); missedAgain != 0 {
		t.Errorf("the cycle after a snap reported %d missed; the catch-up did not settle", missedAgain)
	}
}

// TestNext_SnapStaysOnTheSharedGrid: snapping forward must land on the grid,
// not at "now plus interval". Otherwise every outage silently re-phases the
// monitor and the fleet-wide spread decays with each one.
func TestNext_SnapStaysOnTheSharedGrid(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute}
	phase := Phase("m", minute)

	pending := s.First(at(0))
	// Come back at a deliberately awkward moment, 17.5s into a slot.
	now := pending.Add(10*minute + 17500*time.Millisecond)

	run, _, next := s.Catchup(pending, now, false)

	if offset := time.Duration(run.UnixNano()) % minute; offset != phase {
		t.Errorf("the run slot sits at offset %s in the minute; the grid phase is %s", offset, phase)
	}
	offset := time.Duration(next.UnixNano()) % minute
	if offset != phase {
		t.Fatalf("after snapping, the slot sits at offset %s in the minute; the grid phase is %s", offset, phase)
	}
}

// TestFirst_IsOnTheGridAndNotInThePast covers a monitor that has never run, or
// whose schedule was just reset by a configuration change.
func TestFirst_IsOnTheGridAndNotInThePast(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute}
	phase := Phase("m", minute)

	for _, now := range []time.Time{at(0), at(1), at(59), at(3607), at(86399)} {
		first := s.First(now)
		if first.Before(now) {
			t.Errorf("First(%s) = %s, which is in the past", now, first)
		}
		if first.Sub(now) > minute {
			t.Errorf("First(%s) = %s, more than one interval away", now, first)
		}
		if offset := time.Duration(first.UnixNano()) % minute; offset != phase {
			t.Errorf("First(%s) = %s sits at offset %s, want the grid phase %s", now, first, offset, phase)
		}
	}
}

// TestNext_SuspectPollsFaster: when something looks broken, look again sooner
// (spec §8).
func TestNext_SuspectPollsFaster(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute, SuspectInterval: 10 * time.Second}

	slot := s.First(at(0))
	_, missed, next := s.Catchup(slot, slot.Add(time.Second), true)

	if got := next.Sub(slot); got != 10*time.Second {
		t.Fatalf("a suspect monitor advanced by %s, want the 10s suspect interval", got)
	}
	if missed != 0 {
		t.Errorf("missed = %d, want 0", missed)
	}

	// Not suspect: back to the normal interval.
	_, _, next = s.Catchup(slot, slot.Add(time.Second), false)
	if got := next.Sub(slot); got != minute {
		t.Fatalf("a healthy monitor advanced by %s, want %s", got, minute)
	}
}

// TestNext_SuspectLatenessAlsoDoesNotCompound: the faster cadence gets the
// same I10 guarantee, or a monitor that is slow *and* suspect drifts twice as
// badly as one that is merely slow.
func TestNext_SuspectLatenessAlsoDoesNotCompound(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute, SuspectInterval: 10 * time.Second}

	slot := s.First(at(0))
	for cycle := 0; cycle < 5; cycle++ {
		_, missed, next := s.Catchup(slot, slot.Add(4*time.Second), true)
		if got := next.Sub(slot); got != 10*time.Second {
			t.Fatalf("cycle %d advanced by %s, want 10s", cycle, got)
		}
		if missed != 0 {
			t.Fatalf("cycle %d reported %d missed", cycle, missed)
		}
		slot = next
	}
}

// TestNext_SuspectCatchUpSnapsWithoutReturningToTheSlowGrid: a suspect monitor
// that falls behind must keep its faster cadence. Snapping it onto the normal
// grid would slow it down at exactly the moment it is meant to look more often.
func TestNext_SuspectCatchUpSnapsWithoutReturningToTheSlowGrid(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute, SuspectInterval: 10 * time.Second}

	slot := s.First(at(0))
	now := slot.Add(35 * time.Second) // three suspect intervals behind

	run, missed, next := s.Catchup(slot, now, true)

	if !next.After(now) {
		t.Fatalf("next %s is not after now %s", next, now)
	}
	if got := next.Sub(now); got > 10*time.Second {
		t.Fatalf("the next suspect slot is %s away, longer than the suspect interval — it fell back to the slow grid", got)
	}
	if run.Before(now.Add(-10 * time.Second)) {
		t.Errorf("run slot %s is more than a suspect interval before now %s", run, now)
	}
	if missed != 3 {
		t.Errorf("missed = %d, want 3", missed)
	}
}

// TestResume_ReAlignsToTheSharedGrid: leaving the suspect state must put the
// monitor back on the fleet-wide grid, or a monitor that was briefly suspect
// keeps an off-grid phase forever and the spread decays with every incident.
func TestResume_ReAlignsToTheSharedGrid(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute, SuspectInterval: 10 * time.Second}
	phase := Phase("m", minute)

	resumed := s.Resume(at(0).Add(3 * time.Second))
	if offset := time.Duration(resumed.UnixNano()) % minute; offset != phase {
		t.Fatalf("Resume landed at offset %s, want the grid phase %s", offset, phase)
	}
}

// TestNext_IsPureAndRepeatable. The arithmetic must not depend on hidden
// state — this is what lets the tests above assert on real numbers instead of
// on a fake clock that only advances when asked.
func TestCatchup_IsPureAndRepeatable(t *testing.T) {
	s := Schedule{MonitorID: "m", Interval: minute}
	slot, now := at(600), at(628)

	first, missedFirst, nextFirst := s.Catchup(slot, now, false)
	for i := 0; i < 50; i++ {
		got, missed, next := s.Catchup(slot, now, false)
		if !got.Equal(first) || missed != missedFirst || !next.Equal(nextFirst) {
			t.Fatalf("Catchup is not pure: got (%s, %d, %s) then (%s, %d, %s)", first, missedFirst, nextFirst, got, missed, next)
		}
	}
}
