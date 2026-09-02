package scheduler

import (
	"hash/fnv"
	"time"
)

// Schedule is the scheduling arithmetic for one monitor.
//
// Everything here is a pure function of its arguments — no clock, no timers,
// no state of its own. That split is deliberate and load-bearing: the failure
// this package exists to prevent is compounding lateness, and a test driving a
// fake clock will happily agree with a scheduler that compounds, because the
// fake clock only advances when the scheduler asks it to. Making the
// arithmetic pure means the tests assert on real numbers rather than on a
// clock that was persuaded to cooperate. The waiting lives in Ticker, which is
// thin enough to read in one sitting.
type Schedule struct {
	// MonitorID seeds the phase offset. It must be stable for the monitor's
	// lifetime — this is what makes a restart land on the same grid.
	MonitorID string

	Interval time.Duration

	// SuspectInterval is used while the monitor is in the suspect state: when
	// something looks broken, look again sooner (spec §8). Zero means keep
	// using Interval.
	SuspectInterval time.Duration
}

// Phase is the monitor's deterministic offset within its interval.
//
// Deterministic, not random. Four hundred monitors created at deploy time with
// a 60-second interval otherwise all fire in the same second and hammer the
// access switch once a minute, forever — and a random offset would fix that
// while making the schedule irreproducible, so a monitor would wander between
// restarts and nobody could explain why a run happened when it did.
//
// FNV-1a is chosen for being stable across processes, architectures and Go
// versions. A hash whose value could change between releases would silently
// re-phase an entire fleet on upgrade.
func Phase(monitorID string, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(monitorID))
	return time.Duration(h.Sum64() % uint64(interval))
}

// slotAfter returns the first grid slot strictly after t.
//
// The grid is anchored to the Unix epoch rather than to the monitor's creation
// time, so it does not move when a monitor is edited and two agents computing
// the same monitor's schedule agree without coordinating.
func (s Schedule) slotAfter(t time.Time, interval time.Duration) time.Time {
	phase := Phase(s.MonitorID, interval)
	// Work in integer nanoseconds; floating point here would drift a
	// long-running grid by fractions of a nanosecond per slot, and the whole
	// point of a grid is that it does not drift.
	n := t.UnixNano() - int64(phase)
	k := floorDiv(n, int64(interval)) + 1
	return time.Unix(0, int64(phase)+k*int64(interval))
}

// First is the first slot at or after now, for a monitor that has never run or
// whose schedule was just reset.
func (s Schedule) First(now time.Time) time.Time {
	return s.slotAfter(now.Add(-time.Nanosecond), s.Interval)
}

// Catchup resolves a pending slot that has come due into the run that should
// actually happen now.
//
// It returns the slot that run belongs to, how many slots were skipped to
// reach it, and the next pending slot. The whole of I10 lives here.
//
// **The next slot advances from the previous scheduled value, never from now.**
// A monitor whose run took 28 seconds against a 60-second interval still lands
// on the original grid, because 28 seconds of lateness added to "now" every
// cycle is how a 60-second monitor becomes an 88-second monitor by lunchtime.
//
// **When the agent has been stopped, the schedule snaps forward and reports
// the gap** rather than returning a backlog. Two things follow from that, and
// missing either produces a subtly wrong system:
//
// The run is stamped with the *current* slot, not the stale pending one. A run
// executing now but labelled with a ten-minute-old slot is a backdated run —
// it would record the device's state now while claiming to describe ten
// minutes ago, and it lands in history at a time nothing was actually
// observed. What happened during the gap is unknown, and the honest record of
// that is the missed count, not a fabricated data point.
//
// And only one run is produced, never a queue. Replaying ten minutes of
// 60-second slots would stampede the network at exactly the moment it just
// came back, to collect ten results that are all the same observation wearing
// different timestamps.
func (s Schedule) Catchup(pending, now time.Time, suspect bool) (run time.Time, missed int, next time.Time) {
	interval := s.Interval
	if suspect && s.SuspectInterval > 0 {
		interval = s.SuspectInterval
	}
	if interval <= 0 {
		return pending, 0, pending
	}

	// The common case: the pending slot is the current one, so it runs as
	// scheduled and the next slot is one interval on from it — from the slot,
	// not from now.
	if now.Before(pending.Add(interval)) {
		return pending, 0, pending.Add(interval)
	}

	// Behind. Skip to the most recent slot at or before now, keeping the same
	// cadence so the monitor stays on its grid rather than re-phasing to
	// whenever the agent happened to come back.
	steps := int64(now.Sub(pending) / interval)
	run = pending.Add(time.Duration(steps) * interval)
	return run, int(steps), run.Add(interval)
}

// Resume is the slot to use when a monitor leaves the suspect state and
// returns to its normal cadence.
//
// It re-aligns to the shared grid rather than continuing from wherever the
// faster cadence happened to leave it. Without this a monitor that was briefly
// suspect keeps its off-grid phase forever, and the deterministic spread that
// keeps four hundred monitors from bunching decays a little with every
// incident.
func (s Schedule) Resume(now time.Time) time.Time {
	return s.slotAfter(now, s.Interval)
}

// floorDiv divides rounding towards negative infinity, which is what grid
// arithmetic needs and what Go's / does not do for negative numerators —
// times before the Unix epoch are not realistic here, but a scheduler that
// silently misbehaves for a clock set wrong is worse than one that does not.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
