// Package sandbox is the tier-2 isolate backing the Transform node.
//
// Spec §6.5. Hard memory cap and an enforced CPU deadline with the thread as the real backstop, not a cooperative interrupt. Runs off the main loop so a blocking sandbox cannot stall held sockets, heartbeats or metrics scrapes.
//
// Only serialised data crosses the boundary, never a live host object. The serialisation format must handle bytes and big integers — precisely the two types this node exists for, and precisely the two naive JSON gets wrong. Frame size is gated below the sandbox memory cap before the node runs. Session frame handlers get a much tighter deadline than polled flows.
//
// Decision D10: this arrives late on purpose. If the node catalogue is right the sandbox is rarely needed, and reaching for it is a free signal about which node is missing.
package sandbox
