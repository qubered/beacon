package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/flow/graph"
)

// Inputs holds the resolved input frames for one node execution, keyed by port
// name. A port with no incoming edge, or one whose incoming edges all resolved
// to skipped, is simply absent from the map.
type Inputs map[graph.PortName]frame.Frame

// Outputs holds the frames a node produced, keyed by output port name.
//
// A declared output port absent from this map did not fire this execution —
// the engine marks every edge from that port Skipped and cascades. This is how
// control nodes express a taken branch: If returns Outputs{"true": ...} and
// "false" is simply never present.
type Outputs map[graph.PortName]frame.Frame

// Executable is what a node registers to run. It knows nothing about where it
// executes — Core's local agent and every remote agent share this interface,
// which is what decision D13 requires.
//
// A non-nil error fires the node's implicit error port instead of its declared
// outputs. Returning a *frame.Failure (or anything satisfying it) lets the node
// choose the error class; any other error is wrapped as frame.ClassInternal.
type Executable interface {
	Execute(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error)
}

// Factory constructs the Executable for one graph node.
type Factory func(n graph.Node) (Executable, error)

// PortMeta is the subset of the node catalogue the engine needs, independent
// of how that catalogue is stored. internal/nodes/registry provides the real
// implementation; engine/runtime has no import of it, per the architecture
// rule that the engine does not know what a node does.
type PortMeta interface {
	// Outputs lists every declared output port for nodeType, excluding the
	// implicit error port every node has.
	Outputs(nodeType string) []graph.PortName

	// Required reports whether the named input port must resolve to a
	// delivered frame for the node to run. If every edge into a required port
	// resolves to skipped, the node itself is skipped rather than run with
	// that input absent.
	Required(nodeType string, port graph.PortName) bool

	// Terminal reports whether nodeType is a flow terminus (Emit Status). A run
	// that completes without a terminal node firing produces frame.StatusUnknown
	// plus an execution warning (spec §6.2).
	Terminal(nodeType string) bool
}

// Capturer records one node's execution for the run inspector. Nil is valid —
// Run treats a nil Capturer as "don't capture" rather than requiring a no-op
// implementation at every call site.
type Capturer interface {
	Record(node graph.NodeID, in Inputs, out Outputs, failure *frame.Failure, skipped bool, started, ended time.Time)
}

// RunContext is what every node reads during execution. Nodes may never write
// to it directly; Bind is the one sanctioned mutation and it is engine-owned.
//
// This is the M1 seed of the full context in spec §6.2. Device, monitor and
// persisted state arrive with the agent executor in M3-M4; secret() arrives
// with sealed frames in M7. What's here — identity, vars, deadline, and named
// bindings — is what the DAG executor itself needs regardless of what binds a
// flow to a device.
type RunContext struct {
	RunID       string
	ScheduledAt time.Time
	Attempt     int
	Vars        map[string]any

	mu       sync.RWMutex
	bindings map[string]frame.Frame
}

func NewRunContext(runID string, scheduledAt time.Time) *RunContext {
	return &RunContext{RunID: runID, ScheduledAt: scheduledAt, bindings: map[string]frame.Frame{}}
}

// Bind publishes a node's output under a flow-scoped name (spec §6.2). Called
// by the engine when a node with a Name fires; not for node implementations to
// call directly.
func (rc *RunContext) Bind(name string, f frame.Frame) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.bindings[name] = f
}

// Binding reads a previously published named output. Reading a name that was
// never assigned is a publish-time error under spec §6.2, not a runtime
// concern — by the time a flow runs, every read has already been checked to
// have a matching bind. Binding still reports ok=false defensively.
func (rc *RunContext) Binding(name string) (frame.Frame, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	f, ok := rc.bindings[name]
	return f, ok
}
