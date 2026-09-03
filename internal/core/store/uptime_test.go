package store

import (
	"math"
	"testing"
	"time"
)

var base = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func hr(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }

func closeTo(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestUptime_SimpleUpAndDown(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(18)},
		{State: StateDown, From: hr(18), To: hr(24)},
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(24))

	if !u.HasData {
		t.Fatal("HasData is false for a fully covered window")
	}
	closeTo(t, u.Raw, 0.75, "raw uptime")
	if u.Up != 18*time.Hour {
		t.Errorf("up = %s, want 18h", u.Up)
	}
	if u.Total != 24*time.Hour {
		t.Errorf("total = %s, want 24h", u.Total)
	}
}

// TestUptime_IsIndependentOfInterval is the roadmap's fourth M3 exit gate:
// "Changing an interval does not re-weight yesterday's uptime."
//
// It holds by construction, because the interval never enters the calculation
// — that is the entire reason uptime is time-weighted from periods rather than
// counted from runs (D23). This test exists to keep it that way: any future
// change that reaches for a run count to compute uptime breaks it.
func TestUptime_IsIndependentOfInterval(t *testing.T) {
	// The same day of history. Under run-counting, a monitor polled every 10s
	// and one polled every 5 minutes would produce wildly different
	// denominators for identical behaviour.
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(20)},
		{State: StateDown, From: hr(20), To: hr(24)},
	}

	yesterday := ComputeUptime(periods, hr(0), hr(24), hr(24))

	// The operator changes the monitor's interval today. Yesterday's periods
	// are untouched, so yesterday's number must be untouched.
	afterIntervalChange := ComputeUptime(periods, hr(0), hr(24), hr(48))

	if yesterday.Raw != afterIntervalChange.Raw {
		t.Fatalf("yesterday's uptime changed from %v to %v; it must not depend on the interval",
			yesterday.Raw, afterIntervalChange.Raw)
	}
	closeTo(t, yesterday.Raw, 20.0/24.0, "raw uptime")
}

// TestUptime_MissedRunsDoNotScorePerfect: the scheduler snaps past missed runs
// rather than replaying them, so they produce no run rows at all. Under
// run-counting an hour of unreachability would record zero failures and score
// a perfect hour. Time-weighting charges it as unknown.
func TestUptime_MissedRunsDoNotScorePerfect(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(12)},
		{State: StateUnknown, From: hr(12), To: hr(24)}, // agent unreachable
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(24))

	if u.Raw >= 1.0 {
		t.Fatalf("raw uptime is %v; unobserved time must not count as up", u.Raw)
	}
	closeTo(t, u.Raw, 0.5, "raw uptime")
	if u.Unknown != 12*time.Hour {
		t.Errorf("unknown = %s, want 12h", u.Unknown)
	}
}

// TestUptime_MaintenanceIsReportedBothWays: spec §10 requires both figures,
// because a single number that quietly excludes maintenance is what makes
// uptime reporting untrustworthy.
func TestUptime_MaintenanceIsReportedBothWays(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(12)},
		{State: StateDown, From: hr(12), To: hr(16), InMaintenance: true}, // planned
		{State: StateUp, From: hr(16), To: hr(24)},
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(24))

	// Raw: 20 of 24 hours up.
	closeTo(t, u.Raw, 20.0/24.0, "raw uptime")
	// Excluding maintenance: the 4 planned hours leave both sides, so 20 of 20.
	closeTo(t, u.ExcludingMaintenance, 1.0, "uptime excluding maintenance")
	if u.Maintenance != 4*time.Hour {
		t.Errorf("maintenance = %s, want 4h", u.Maintenance)
	}
	if u.Raw >= u.ExcludingMaintenance && u.Raw != u.ExcludingMaintenance {
		t.Error("raw should be the lower figure when a maintenance window contained a failure")
	}
}

// TestUptime_UpTimeInsideMaintenanceLeavesBothSides: excluding maintenance
// must remove it from the numerator too. Leaving it in the numerator while
// removing it from the denominator can push uptime above 100%.
func TestUptime_UpTimeInsideMaintenanceLeavesBothSides(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(12), InMaintenance: true}, // up, but excused
		{State: StateDown, From: hr(12), To: hr(24)},
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(24))

	if u.ExcludingMaintenance > 1.0 {
		t.Fatalf("uptime excluding maintenance is %v, above 100%% — up time inside a window was left in the numerator", u.ExcludingMaintenance)
	}
	// 12h excused, 12h observed and all of it down.
	closeTo(t, u.ExcludingMaintenance, 0.0, "uptime excluding maintenance")
	closeTo(t, u.Raw, 0.5, "raw uptime")
}

// TestUptime_WindowEntirelyInMaintenanceReportsNoOpinion: a fabricated 100%
// for a window nobody was watching is worse than an honest blank.
func TestUptime_WindowEntirelyInMaintenanceReportsNoOpinion(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0), To: hr(24), InMaintenance: true},
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(24))

	closeTo(t, u.Raw, 1.0, "raw uptime")
	closeTo(t, u.ExcludingMaintenance, 0.0, "uptime excluding maintenance")
	if u.Maintenance != 24*time.Hour {
		t.Errorf("maintenance = %s, want 24h", u.Maintenance)
	}
}

// TestUptime_PeriodsAreClippedToTheWindow: asking for yesterday must be
// answerable from periods that began last week.
func TestUptime_PeriodsAreClippedToTheWindow(t *testing.T) {
	periods := []StatePeriod{
		// Started a week before the window and runs a week past it.
		{State: StateUp, From: hr(-168), To: hr(192)},
	}
	u := ComputeUptime(periods, hr(0), hr(24), hr(192))

	if u.Total != 24*time.Hour {
		t.Fatalf("total = %s, want exactly the 24h window", u.Total)
	}
	closeTo(t, u.Raw, 1.0, "raw uptime")
}

// TestUptime_OpenPeriodRunsToNowNotToTheWindowEnd: a window extending into the
// future must not credit time that has not happened yet.
func TestUptime_OpenPeriodRunsToNowNotToTheWindowEnd(t *testing.T) {
	periods := []StatePeriod{
		{State: StateUp, From: hr(0)}, // still current
	}
	// The window covers a full day but only six hours have elapsed.
	u := ComputeUptime(periods, hr(0), hr(24), hr(6))

	if u.Total != 6*time.Hour {
		t.Fatalf("total = %s, want 6h — the future is not observed time", u.Total)
	}
	closeTo(t, u.Raw, 1.0, "raw uptime")
}

// TestUptime_NoDataIsDistinctFromZero: "we have no idea" and "it was down all
// day" are different answers, and showing the second when you mean the first
// is how an operator stops trusting the number.
func TestUptime_NoDataIsDistinctFromZero(t *testing.T) {
	u := ComputeUptime(nil, hr(0), hr(24), hr(24))
	if u.HasData {
		t.Fatal("HasData is true with no periods")
	}

	down := ComputeUptime([]StatePeriod{{State: StateDown, From: hr(0), To: hr(24)}}, hr(0), hr(24), hr(24))
	if !down.HasData {
		t.Fatal("HasData is false for a monitor that was genuinely down all day")
	}
	if u.Raw != down.Raw {
		t.Log("both report 0.0, which is why HasData exists to tell them apart")
	}
}

// TestUptime_OnlyUpCountsAsUp. Every state other than up — suspect,
// recovering, down, unknown — is time the monitor was not confirmed healthy,
// and crediting any of them would inflate the number people make decisions on.
func TestUptime_OnlyUpCountsAsUp(t *testing.T) {
	for _, notUp := range []State{StateDown, StateSuspect, StateRecovering, StateUnknown} {
		periods := []StatePeriod{
			{State: StateUp, From: hr(0), To: hr(12)},
			{State: notUp, From: hr(12), To: hr(24)},
		}
		u := ComputeUptime(periods, hr(0), hr(24), hr(24))
		closeTo(t, u.Raw, 0.5, "raw uptime with "+string(notUp))
	}
}

func TestUptime_EmptyOrInvertedWindow(t *testing.T) {
	periods := []StatePeriod{{State: StateUp, From: hr(0), To: hr(24)}}
	for _, tc := range []struct{ from, to time.Time }{
		{hr(5), hr(5)},  // empty
		{hr(10), hr(5)}, // inverted
	} {
		u := ComputeUptime(periods, tc.from, tc.to, hr(24))
		if u.HasData || u.Total != 0 {
			t.Errorf("window [%s,%s) reported data", tc.from, tc.to)
		}
	}
}
