package runtime

import (
	"errors"
	"time"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/flow/graph"
)

// NodeState is the terminal state a node reached during a run.
type NodeState string

const (
	NodeDone    NodeState = "done"
	NodeError   NodeState = "error"
	NodeSkipped NodeState = "skipped"
)

// NodeRun is one node's contribution to a Result, independent of whether
// capture is enabled — this is what makes "did the graph do the right thing"
// testable without standing up the capture package.
type NodeRun struct {
	Node    graph.NodeID
	State   NodeState
	Outputs Outputs
	Failure *frame.Failure
	Started time.Time
	Ended   time.Time
}

// Result is what one flow run produced.
//
// Status and Warning implement spec §6.2's rule directly: a run that never
// reaches an Emit Status node produces StatusUnknown plus a non-empty Warning,
// rather than an error — the run itself succeeded as an execution, it simply
// never arrived at a verdict.
type Result struct {
	Status  frame.Status
	Warning string

	Nodes map[graph.NodeID]NodeRun

	// Err is set only when the RUN failed outright: an unconnected error port
	// fired, the node budget was exceeded, or the deadline elapsed with work
	// still outstanding. Nodes still carries every node that ran before the
	// failure, for the capture the operator needs to explain it.
	Err error
}

// ErrDeadlineExceeded, ErrNodeBudgetExceeded and ErrUnhandledFailure are the
// three ways invariant I2 or an unconnected error port ends a run. Each is
// wrapped with the triggering node and, for ErrUnhandledFailure, the
// frame.Failure that caused it.
var (
	ErrDeadlineExceeded   = errors.New("run exceeded its wall-clock deadline")
	ErrNodeBudgetExceeded = errors.New("run exceeded its node-execution budget")
)

// UnhandledFailure wraps a Failure that reached a node with no wired error
// port, per spec §6.2: "Unconnected, an error propagates and fails the run
// with full context captured."
type UnhandledFailure struct {
	Node    graph.NodeID
	Failure frame.Failure
}

func (e *UnhandledFailure) Error() string {
	// Failure.Error() already includes the node name when Failure.Node is
	// set, which it always is by the time this wraps it (toFailure fills it
	// in) — prefixing it again here read as "unhandled error at check: check:
	// battery run time critically low (assertion)".
	return "unhandled error: " + e.Failure.Error()
}

func (e *UnhandledFailure) Unwrap() error { return e.Failure }
