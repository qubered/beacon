// Package ingest accepts batched results from agents and replays the alert state machine over backfill.
//
// Spec §7.3. Backfill must not page anyone. Core replays the state machine so history and uptime are correct — the incident record shows the device went down at 19:42, not at reconnect time — and suppresses notifications for state changes older than a configured age, emitting one summary instead.
//
// Spec §7.7: liveness is judged against Core's receipt time, never the agent's claimed timestamp, so a skewed agent cannot fake being alive. Results with implausible timestamps are rejected and counted.
package ingest
