// Package alerting is the alert state machine, incidents, suppression and delivery dispatch. It stays central because dependency suppression is inherently cross-agent.
//
// Spec §11. Everything here exists to prevent one outcome: a tech who gets a false page at 11pm turns the system off. Ship conservative and tighten later, never the reverse.
//
// The machine is up -> suspect -> down -> recovering -> up, persisted, asymmetric by design. Going down requires both n consecutive failures and a minimum dwell time — at a five-second interval, three failures is fifteen seconds, far too twitchy for a human notification. Transitions are a stored boolean on the run row, not alerting-layer logic, so alert history is queryable.
//
// Flapping is a weighted state-change percentage over a time-based window with two thresholds and hysteresis, computed from a fixed-size ring on the monitor row rather than by querying the partitioned run table. The flap percentage is surfaced in the UI as a first-class number: a monitor sitting at 40% almost always indicts a marginal patch lead, a saturated uplink or a PoE budget problem.
//
// Invariant I12: maintenance windows and silences suppress notification, never collection. Invariant I8: backfilled state changes older than a configured age update history but do not notify. Invariant I9: an unreachable agent's devices go unknown and suppressed, never down — one alert, not sixty.
//
// Decision D24: the state machine is in-app, notification delivery is outsourced to a fan-out service addressed by URL scheme, with a plain webhook always available as a fallback.
package alerting
