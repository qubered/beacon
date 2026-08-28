package frame

import (
	"time"

	"github.com/qubered/beacon/internal/flow/types"
)

// Origin identifies where a frame came from, for the run inspector.
//
// Nodes never set this themselves — Executable.Execute has no access to its
// own node ID, deliberately, since a node has no business knowing where it
// sits in the graph. internal/engine/runtime stamps Origin and ProducedAt on
// every frame a node returns, right after Execute returns, which is also the
// one place that can't get it wrong.
type Origin struct {
	NodeID string
	Port   string
}

// Frame is the typed value travelling on a wire between two nodes (spec §6.1).
//
// Frames are immutable once produced. Fan-out shares the same Frame rather than
// copying it, so nothing downstream may mutate Value.
type Frame struct {
	Type  types.Type
	Value any

	Origin     Origin
	ProducedAt time.Time

	// Truncated is set when the captured representation of this frame was cut to
	// the configured capture size. The value itself is never truncated.
	Truncated bool

	// Sealed marks a frame that derives from a secret (invariant I4). A sealed
	// frame is non-capturable, non-exportable, unreadable from an expression or
	// the sandbox, and consumable only by transports, hashing, HMAC and payload
	// composition.
	//
	// Sealing is transitive: anything derived from a sealed frame stays sealed
	// until it passes through a declared one-way function. See internal/secrets.
	Sealed bool
}

// Derive produces a new frame from this one, carrying the seal forward. Origin
// and ProducedAt are left zero — internal/engine/runtime stamps both once the
// frame reaches it, so a node need not and cannot mis-stamp them.
//
// Use this for every node output computed from an input. A node that constructs
// a Frame literal from sealed input silently launders a secret into a capture,
// which is exactly the bug invariant I4 exists to prevent.
func (f Frame) Derive(t types.Type, v any) Frame {
	return Frame{Type: t, Value: v, Sealed: f.Sealed}
}

// Unseal marks a frame as no longer secret-derived. It is legal only after a
// one-way function (a cryptographic hash or HMAC) and callers outside
// internal/secrets and the hashing nodes should not use it.
func (f Frame) Unseal() Frame {
	f.Sealed = false
	return f
}
