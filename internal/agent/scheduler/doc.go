// Package scheduler is the per-agent scheduler, ticking against local state.
//
// Decision D14 and spec §8. Core owns what to monitor; the agent owns when and how, so monitoring does not stop the instant the link drops.
//
// Deterministic jitter: each monitor's phase offset is a hash of its id modulo its interval, so the schedule is reproducible and a restart lands on the same grid. Without it, four hundred monitors created at deploy time with a 60s interval fire in the same second forever.
//
// Invariant I10: lateness never compounds. The next run time advances from the previous scheduled value, never from now.
//
// Catch-up snaps forward and records a missed-runs gap; it never replays backdated runs. Configuration changes reset the schedule and suppress the missed-run increment for the first tick. Suspect monitors poll faster.
package scheduler
