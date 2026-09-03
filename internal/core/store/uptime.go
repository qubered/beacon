package store

import (
	"time"
)

// State is a monitor's alert state over a period. It mirrors the monitor_state
// enum in migration 0001.
type State string

// These are the alert state machine's states (spec §11), and they are exactly
// the monitor_state enum in migration 0001 — TestState_MatchesTheDatabaseEnum
// keeps the two from drifting.
//
// Note what is absent: `degraded`. That is a *run* status, not an alert state,
// so a persistently degraded monitor currently holds an `up` period and counts
// as fully up in uptime. Whether the alert state machine should gain a
// degraded state is an M5 question — see docs/ROADMAP.md — and adding the
// constant here before the enum has it would only produce writes the database
// rejects.
const (
	StateUnknown    State = "unknown"
	StateUp         State = "up"
	StateDown       State = "down"
	StateSuspect    State = "suspect"
	StateRecovering State = "recovering"
)

// StatePeriod is one row of monitor_state_periods: a monitor held one state
// from one instant to another. An open period has a zero To.
//
// These are the source of truth for uptime (D23), not the run rows.
type StatePeriod struct {
	State         State
	From          time.Time
	To            time.Time // zero means still current
	InMaintenance bool
}

// Uptime is the answer, reported two ways because operators will ask for both
// and a single number that quietly excludes maintenance is exactly what makes
// uptime reporting untrustworthy (spec §10).
type Uptime struct {
	// Raw counts every second in the window, maintenance included.
	Raw float64
	// ExcludingMaintenance removes maintenance time from both the numerator
	// and the denominator, so a window spent entirely in maintenance reports
	// no opinion rather than 100%.
	ExcludingMaintenance float64

	// The durations behind the ratios, so a caller can show "4h of 24h in
	// maintenance" rather than only a percentage.
	Total       time.Duration
	Up          time.Duration
	Maintenance time.Duration
	Unknown     time.Duration

	// HasData is false when the window contains no observed time at all. A
	// caller must not render 0% for this: "we have no idea" and "it was down
	// all day" are different answers, and showing the second when you mean the
	// first is how an operator loses trust in the number.
	HasData bool
}

// ComputeUptime time-weights state periods across [from, to).
//
// Time-weighted from periods, never counted from runs (D23). Run-count uptime
// is broken here by construction: change a monitor's interval and yesterday's
// failures re-weight, because the denominator is a run count that just changed
// meaning. Missed runs produce no row at all, since the scheduler snaps past
// them rather than replaying — so a monitor that was unreachable for an hour
// would score a *perfect* run-counted uptime for that hour, having recorded no
// failures. Throttled and unknown runs have no obvious weight. Time-weighting
// sidesteps every one of those.
//
// This is deliberately a pure function over periods rather than a SQL
// aggregate. The arithmetic is where the subtleties live — clipping to the
// window, open periods, maintenance overlapping a state change — and it is
// worth being able to test all of that without a database, exhaustively, in
// microseconds.
func ComputeUptime(periods []StatePeriod, from, to time.Time, now time.Time) Uptime {
	var u Uptime
	if !to.After(from) {
		return u
	}

	for _, p := range periods {
		start, end := p.From, p.To
		// An open period runs to now, not to the end of the window — a window
		// extending into the future must not credit time that has not happened.
		if end.IsZero() {
			end = now
		}
		// Clip to the window. A period straddling either edge contributes only
		// its overlap, which is what makes "uptime for yesterday" answerable
		// from periods that started the week before.
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if !end.After(start) {
			continue
		}
		d := end.Sub(start)

		u.Total += d
		switch {
		case p.InMaintenance:
			u.Maintenance += d
		case p.State == StateUnknown:
			u.Unknown += d
		}
		if p.State == StateUp {
			u.Up += d
		}
	}

	if u.Total <= 0 {
		return u
	}
	u.HasData = true

	// Unknown time counts against raw uptime rather than being excluded. That
	// is the honest treatment: "we could not tell" is not the same as "it was
	// fine", and a monitor whose agent was unreachable all afternoon should not
	// report 100%.
	u.Raw = ratio(u.Up, u.Total)

	observed := u.Total - u.Maintenance
	if observed > 0 {
		// Maintenance leaves both numerator and denominator. Up time recorded
		// during a window is not counted as credit either, so a monitor is
		// neither rewarded nor punished for a window it was excused from.
		upOutside := u.Up - maintenanceUp(periods, from, to, now)
		if upOutside < 0 {
			upOutside = 0
		}
		u.ExcludingMaintenance = ratio(upOutside, observed)
	} else {
		// The whole window was maintenance. Report no opinion rather than a
		// fabricated 100% or 0%.
		u.ExcludingMaintenance = 0
	}

	return u
}

// maintenanceUp is the up-time that fell inside a maintenance window, which
// has to come out of the numerator when maintenance comes out of the
// denominator.
func maintenanceUp(periods []StatePeriod, from, to, now time.Time) time.Duration {
	var d time.Duration
	for _, p := range periods {
		if !p.InMaintenance || p.State != StateUp {
			continue
		}
		start, end := p.From, p.To
		if end.IsZero() {
			end = now
		}
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if end.After(start) {
			d += end.Sub(start)
		}
	}
	return d
}

func ratio(part, whole time.Duration) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}
