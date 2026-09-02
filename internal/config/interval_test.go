package config

import (
	"strings"
	"testing"
	"time"
)

// TestCheckInterval_RefusesTheFootgunButLeavesAnAdminRoute is the decision
// settled before M3: a five-second floor, with anything below it admin-only.
func TestCheckInterval_RefusesTheFootgunButLeavesAnAdminRoute(t *testing.T) {
	p := DefaultIntervalPolicy()

	// The case the floor exists for: a tech types 1s into a form.
	err := p.CheckInterval(time.Second, false)
	if err == nil {
		t.Fatal("a one-second interval was accepted for a non-admin")
	}
	if !strings.Contains(err.Error(), "administrator") {
		t.Errorf("refusal %q does not say what would lift it — a refusal must carry a suggestion", err)
	}

	// The same interval, from someone who has deliberately taken the
	// privileged route.
	if err := p.CheckInterval(time.Second, true); err != nil {
		t.Fatalf("an admin was refused the override: %v", err)
	}

	if err := p.CheckInterval(5*time.Second, false); err != nil {
		t.Errorf("the floor itself was refused: %v", err)
	}
	if err := p.CheckInterval(time.Minute, false); err != nil {
		t.Errorf("an ordinary interval was refused: %v", err)
	}
}

// TestCheckInterval_AbsoluteFloorIsNotOverridable: the admin route lowers the
// floor, it does not remove it. Sub-second polling is a denial of service
// against the gear whoever asked for it.
func TestCheckInterval_AbsoluteFloorIsNotOverridable(t *testing.T) {
	p := DefaultIntervalPolicy()
	for _, d := range []time.Duration{time.Millisecond, 999 * time.Millisecond} {
		err := p.CheckInterval(d, true)
		if err == nil {
			t.Fatalf("an admin was allowed a %s interval, below the absolute floor", d)
		}
		if !strings.Contains(err.Error(), "cannot be overridden") {
			t.Errorf("refusal %q does not make clear the floor is absolute", err)
		}
	}
}

func TestCheckInterval_RejectsNonPositive(t *testing.T) {
	p := DefaultIntervalPolicy()
	for _, d := range []time.Duration{0, -time.Second} {
		if err := p.CheckInterval(d, true); err == nil {
			t.Errorf("interval %s was accepted", d)
		}
	}
}

// TestIntervalPolicy_ClampKeepsAConfigurationWorking mirrors Bounds.Clamp: a
// value that drifted should keep running under a sane floor rather than stop
// the process.
func TestIntervalPolicy_ClampKeepsAConfigurationWorking(t *testing.T) {
	p, notes := IntervalPolicy{MinInterval: time.Millisecond}.Clamp()
	if p.MinInterval != AbsoluteMinInterval {
		t.Errorf("MinInterval = %s, want the absolute floor %s", p.MinInterval, AbsoluteMinInterval)
	}
	if len(notes) == 0 {
		t.Error("the adjustment was silent; an operator needs to be told their floor was raised")
	}

	p, _ = IntervalPolicy{}.Clamp()
	if p.MinInterval != DefaultMinInterval {
		t.Errorf("an unset floor became %s, want the %s default", p.MinInterval, DefaultMinInterval)
	}

	// A deliberately lowered but legal floor is left alone.
	p, notes = IntervalPolicy{MinInterval: 2 * time.Second}.Clamp()
	if p.MinInterval != 2*time.Second || len(notes) != 0 {
		t.Errorf("a legal configured floor was altered to %s (%v)", p.MinInterval, notes)
	}
}

// TestAbsoluteFloor_MatchesTheDatabaseConstraint. Migration 0003 encodes 1000ms
// as a CHECK. If these two drift, the database refuses something the
// application accepted and the failure surfaces as an insert error nobody can
// explain from the UI.
func TestAbsoluteFloor_MatchesTheDatabaseConstraint(t *testing.T) {
	if AbsoluteMinInterval != 1000*time.Millisecond {
		t.Fatalf("AbsoluteMinInterval is %s but migration 0003 checks interval_ms >= 1000", AbsoluteMinInterval)
	}
}
