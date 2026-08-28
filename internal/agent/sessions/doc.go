// Package sessions is the connection supervisor for session-mode connections.
//
// Spec §9. Per connection: a connecting -> open -> degraded -> closed state machine, exponential backoff with jitter, a circuit breaker, an on-open flow for devices that need a command re-sent after every reconnect, and an optional keepalive poke.
//
// The staleness rule is the whole reconciliation: healthy(source) iff now - last_frame_at < liveness_window. A push source that stops pushing becomes indistinguishable from a poll target that stops answering, which makes alerting, suppression, uptime and metrics uniform across both models.
//
// Backpressure: a device in a fault loop emits thousands of messages per second. Rate-limit per connection, count drops, alert on the drop rate. Frame handlers get a tighter execution budget and a per-connection execution rate cap.
//
// Connections are themselves monitorable. Operators ask "is Beacon connected?" before "is the device up?".
package sessions
