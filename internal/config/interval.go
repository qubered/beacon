package config

import (
	"fmt"
	"time"
)

// Monitor interval floors.
//
// A one-second interval is a footgun that will knock over someone's receiver.
// But a hard floor with no way past it just relocates the problem: someone will
// eventually have robust gear and a real reason to poll faster, and their only
// route would be a code change. So there are two numbers.
const (
	// DefaultMinInterval is the floor a normal user gets. Five seconds is slow
	// enough that a mistyped interval cannot hammer a device and fast enough
	// that nobody reaches for the override casually.
	DefaultMinInterval = 5 * time.Second

	// AbsoluteMinInterval is the floor nobody may cross, admin included. It is
	// also a CHECK constraint in migration 0003, so it holds even against a
	// direct database write.
	AbsoluteMinInterval = time.Second
)

// IntervalPolicy is the configured floor and who may go below it.
type IntervalPolicy struct {
	// MinInterval is the floor for ordinary monitor creation and editing.
	MinInterval time.Duration
}

func DefaultIntervalPolicy() IntervalPolicy {
	return IntervalPolicy{MinInterval: DefaultMinInterval}
}

// Clamp raises a non-positive or below-absolute floor to something legal and
// reports what it changed, in the same spirit as Bounds.Clamp: a configuration
// that drifted should keep working under a sane value rather than stop.
func (p IntervalPolicy) Clamp() (IntervalPolicy, []string) {
	var notes []string
	if p.MinInterval <= 0 {
		p.MinInterval = DefaultMinInterval
	}
	if p.MinInterval < AbsoluteMinInterval {
		notes = append(notes, fmt.Sprintf(
			"minimum monitor interval %s is below the absolute floor %s and was raised",
			p.MinInterval, AbsoluteMinInterval))
		p.MinInterval = AbsoluteMinInterval
	}
	return p, notes
}

// CheckInterval validates a proposed monitor interval.
//
// isAdmin is passed explicitly rather than read from a context or a session
// object, for the same reason store methods take a site.ID explicitly: the
// privileged path should be visible at every call site, so nobody grants the
// override by forgetting to pass something.
//
// The refusal names the floor and says what would lift it, per the repository's
// rule that a refusal must carry a suggestion — "interval too low" tells an
// operator nothing they can act on.
func (p IntervalPolicy) CheckInterval(interval time.Duration, isAdmin bool) error {
	if interval <= 0 {
		return fmt.Errorf("monitor interval must be positive")
	}
	if interval < AbsoluteMinInterval {
		return fmt.Errorf(
			"monitor interval %s is below the absolute floor of %s, which cannot be overridden — "+
				"a device polled faster than this will be knocked over",
			interval, AbsoluteMinInterval)
	}
	if interval < p.MinInterval && !isAdmin {
		return fmt.Errorf(
			"monitor interval %s is below the %s minimum; an administrator can set a lower interval "+
				"for gear that tolerates it",
			interval, p.MinInterval)
	}
	return nil
}
