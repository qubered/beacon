// Package runtime is the flow executor. There is exactly one implementation, used identically by Core's local agent and by every remote agent (decision D13).
//
// Invariant I2: every run terminates. Wall-clock deadline, node-execution budget and loop caps are enforced, and the deadline reaches the socket — a timeout that only checks the clock between nodes is not a timeout.
//
// Firing rules (spec §6.1, §6.2): nodes fire when inputs are satisfied; independent branches run concurrently up to a per-flow limit; fan-out shares an immutable value; fan-in is refused except on variadic ports; and a branch join is a select — the first branch to settle supplies the frame, and readiness means all reachable upstream branches have settled, not all connected inputs have a frame. Without that last rule an If join deadlocks forever.
//
// Every node has an implicit error output port. Unconnected, an error fails the run with full context. Connected, the error frame flows down that branch and the run continues.
package runtime
