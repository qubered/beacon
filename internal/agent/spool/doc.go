// Package spool is the durable local outbound spool, bounded by both size and age.
//
// Spec §7.3 and invariant I6. On overflow, shed captures before shedding results: a capture is the large payload and the least load-bearing, while status and metric rows are what uptime and history are computed from. Count both drop classes.
//
// Delivery is at-least-once with acknowledgement. Each result carries its original scheduled slot, which doubles as the execution fence, so replays deduplicate on insert rather than double-counting.
package spool
