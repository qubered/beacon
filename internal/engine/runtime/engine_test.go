package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
)

// --- test fixtures: a tiny fake catalog standing in for internal/nodes/registry ---

type fakeExec func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error)

func (f fakeExec) Execute(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
	return f(ctx, rc, in)
}

// fakeMeta describes a small fixed set of node types used across these tests:
//
//	"const"    - out: any             (no inputs; always fires "out")
//	"if"       - in: any -> true/false (fires exactly one, per its Config)
//	"join"     - a,b: any -> out       (a branch-join target; a and b both feed one node)
//	"status"   - in: status            (terminal)
//	"slow"     - out: any              (blocks until ctx.Done or a fixed delay)
//	"failing"  - out: any              (always errors)
type fakeMeta struct{}

func (fakeMeta) Outputs(nodeType string) []graph.PortName {
	switch nodeType {
	case "const", "slow", "failing":
		return []graph.PortName{"out"}
	case "if":
		return []graph.PortName{"true", "false"}
	case "join":
		return []graph.PortName{"out"}
	case "status":
		return nil
	}
	return nil
}

func (fakeMeta) Required(nodeType string, port graph.PortName) bool {
	// The join node's two inputs are the branch-join case: neither is
	// required on its own, because exactly one is expected to resolve to a
	// value and the other to skip. If both were "required", the skipped one
	// would take the node down instead of the join proceeding on the live one.
	if nodeType == "join" {
		return false
	}
	return true
}

func (fakeMeta) Terminal(nodeType string) bool { return nodeType == "status" }

func constNode(v any) Executable {
	return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
		return Outputs{"out": frame.Frame{Type: types.Any(), Value: v}}, nil
	})
}

func joinNode() Executable {
	return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
		for _, port := range []graph.PortName{"a", "b"} {
			if f, ok := in[port]; ok {
				return Outputs{"out": f}, nil
			}
		}
		return Outputs{}, nil
	})
}

func statusNode(s frame.Status) Executable {
	return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
		return Outputs{}, nil // "status" has no declared outputs in fakeMeta; value asserted via context below
	})
}

// ifNode fires exactly one of its two outputs based on cond.
func ifNode(cond bool) Executable {
	return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
		if cond {
			return Outputs{"true": frame.Frame{Type: types.Void()}}, nil
		}
		return Outputs{"false": frame.Frame{Type: types.Void()}}, nil
	})
}

func defaultBounds() config.Bounds {
	b := config.DefaultBounds()
	b.RunWallClock = 5 * time.Second
	return b
}

// --- the branch-join deadlock test: the single most important correctness
// property in this package ---

func TestBranchJoin_UntakenBranchDoesNotDeadlock(t *testing.T) {
	// if --true--> a --\
	//   --false-> b --> join --> (nothing further; run ends when join is done)
	// The If takes the true branch, so "b" is never wired to fire (no incoming
	// control edge in this fake setup reaches it — its readiness comes purely
	// from having zero inputs, so it WOULD run regardless in a real engine
	// with control-gated activation. To exercise the actual join semantics
	// this test wires "a" and "b" as two producers feeding "join", and "b" is
	// downstream of the If's false branch via a void gate that never fires.
	g := &graph.Graph{
		Nodes: []graph.Node{
			{ID: "cond", Type: "if"},
			{ID: "a", Type: "const"},
			{ID: "b", Type: "const"},
			{ID: "join", Type: "join"},
		},
		Edges: []graph.Edge{
			// "b" only produces once gated by the If's false output. Since the
			// If fires true, "false" never fires, so the edge into b's gate
			// resolves to skipped, which — because "in" is required on
			// "const" — takes "b" itself down as skipped.
			{From: graph.Endpoint{Node: "cond", Port: "false"}, To: graph.Endpoint{Node: "b", Port: "in"}},
			{From: graph.Endpoint{Node: "cond", Port: "true"}, To: graph.Endpoint{Node: "a", Port: "in"}},
			{From: graph.Endpoint{Node: "a", Port: "out"}, To: graph.Endpoint{Node: "join", Port: "a"}},
			{From: graph.Endpoint{Node: "b", Port: "out"}, To: graph.Endpoint{Node: "join", Port: "b"}},
		},
	}

	// "const" here needs an "in" input port too for the gate wiring above to
	// make sense; extend fakeMeta's Required just for this test via a local
	// override is unnecessary — "const" nodes ignore inputs entirely and
	// Required defaults to true for an unknown (nodeType,port) pair under
	// fakeMeta, which is exactly the behavior under test: b's gate is
	// required, doesn't arrive, and b is skipped rather than the run hanging.

	factory := func(n graph.Node) (Executable, error) {
		switch n.Type {
		case "if":
			return ifNode(true), nil
		case "const":
			return constNode(n.ID), nil
		case "join":
			return joinNode(), nil
		}
		t.Fatalf("unexpected node type %s", n.Type)
		return nil, nil
	}

	rc := NewRunContext("run-1", time.Now())
	res := Run(context.Background(), g, factory, fakeMeta{}, defaultBounds(), rc, nil)

	if res.Err != nil {
		t.Fatalf("run must complete without hanging or failing, got err: %v", res.Err)
	}
	if res.Nodes["b"].State != NodeSkipped {
		t.Fatalf("b (untaken branch) should be skipped, got %s", res.Nodes["b"].State)
	}
	if res.Nodes["a"].State != NodeDone {
		t.Fatalf("a (taken branch) should be done, got %s", res.Nodes["a"].State)
	}
	joinRun, ok := res.Nodes["join"]
	if !ok || joinRun.State != NodeDone {
		t.Fatalf("join must fire once its one reachable branch settles, got %+v", res.Nodes["join"])
	}
	if got := joinRun.Outputs["out"].Value; got != graph.NodeID("a") {
		t.Fatalf("join should carry the value from the branch that actually settled, got %v", got)
	}
}

func TestBranchJoin_OtherDirectionAlsoResolves(t *testing.T) {
	g := &graph.Graph{
		Nodes: []graph.Node{
			{ID: "cond", Type: "if"},
			{ID: "a", Type: "const"},
			{ID: "b", Type: "const"},
			{ID: "join", Type: "join"},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "cond", Port: "false"}, To: graph.Endpoint{Node: "b", Port: "in"}},
			{From: graph.Endpoint{Node: "cond", Port: "true"}, To: graph.Endpoint{Node: "a", Port: "in"}},
			{From: graph.Endpoint{Node: "a", Port: "out"}, To: graph.Endpoint{Node: "join", Port: "a"}},
			{From: graph.Endpoint{Node: "b", Port: "out"}, To: graph.Endpoint{Node: "join", Port: "b"}},
		},
	}
	factory := func(n graph.Node) (Executable, error) {
		switch n.Type {
		case "if":
			return ifNode(false), nil
		case "const":
			return constNode(n.ID), nil
		case "join":
			return joinNode(), nil
		}
		return nil, nil
	}
	res := Run(context.Background(), g, factory, fakeMeta{}, defaultBounds(), NewRunContext("r", time.Now()), nil)
	if res.Err != nil {
		t.Fatalf("unexpected run error: %v", res.Err)
	}
	if res.Nodes["a"].State != NodeSkipped || res.Nodes["b"].State != NodeDone {
		t.Fatalf("expected a skipped / b done, got a=%s b=%s", res.Nodes["a"].State, res.Nodes["b"].State)
	}
	if got := res.Nodes["join"].Outputs["out"].Value; got != graph.NodeID("b") {
		t.Fatalf("join should carry b's value, got %v", got)
	}
}

// --- error ports ---

func TestErrorPort_UnconnectedFailsTheRun(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{{ID: "boom", Type: "failing"}}}
	factory := func(n graph.Node) (Executable, error) {
		return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
			return nil, frame.Fail(frame.ClassTimeout, "device did not respond")
		}), nil
	}
	res := Run(context.Background(), g, factory, fakeMeta{}, defaultBounds(), NewRunContext("r", time.Now()), nil)

	if res.Err == nil {
		t.Fatal("an unconnected error port must fail the whole run")
	}
	var uf *UnhandledFailure
	if !asUnhandled(res.Err, &uf) {
		t.Fatalf("expected *UnhandledFailure, got %T: %v", res.Err, res.Err)
	}
	if uf.Failure.Class != frame.ClassTimeout {
		t.Fatalf("failure class should carry through, got %s", uf.Failure.Class)
	}
	if res.Nodes["boom"].State != NodeError {
		t.Fatalf("the failing node itself should still be captured as errored, got %s", res.Nodes["boom"].State)
	}
}

func asUnhandled(err error, target **UnhandledFailure) bool {
	if uf, ok := err.(*UnhandledFailure); ok {
		*target = uf
		return true
	}
	return false
}

func TestErrorPort_ConnectedContinuesTheRun(t *testing.T) {
	// failing --error--> fallback(join) --out--> (done)
	g := &graph.Graph{
		Nodes: []graph.Node{
			{ID: "boom", Type: "failing"},
			{ID: "handler", Type: "join"}, // reuse "join": it just passes through whatever port fired
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "boom", Port: "error"}, To: graph.Endpoint{Node: "handler", Port: "a"}},
		},
	}
	factory := func(n graph.Node) (Executable, error) {
		if n.Type == "failing" {
			return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
				return nil, frame.Fail(frame.ClassProtocol, "unexpected reply")
			}), nil
		}
		return joinNode(), nil
	}
	res := Run(context.Background(), g, factory, fakeMeta{}, defaultBounds(), NewRunContext("r", time.Now()), nil)

	if res.Err != nil {
		t.Fatalf("a connected error port must let the run continue, got Err: %v", res.Err)
	}
	if res.Nodes["boom"].State != NodeError {
		t.Fatalf("boom should still be recorded as errored, got %s", res.Nodes["boom"].State)
	}
	h, ok := res.Nodes["handler"]
	if !ok || h.State != NodeDone {
		t.Fatalf("handler should run, consuming the error frame, got %+v", res.Nodes["handler"])
	}
	fail, ok := h.Outputs["out"].Value.(frame.Failure)
	if !ok || fail.Class != frame.ClassProtocol {
		t.Fatalf("handler should have received the Failure as its input value, got %#v", h.Outputs["out"].Value)
	}
}

// --- bounds: deadline and node budget (invariant I2) ---

func TestDeadline_TerminatesASlowRun(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{{ID: "slow", Type: "slow"}}}
	factory := func(n graph.Node) (Executable, error) {
		return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
			select {
			case <-time.After(2 * time.Second):
				return Outputs{"out": frame.Frame{Type: types.Any()}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), nil
	}

	bounds := defaultBounds()
	bounds.RunWallClock = 100 * time.Millisecond

	start := time.Now()
	res := Run(context.Background(), g, factory, fakeMeta{}, bounds, NewRunContext("r", time.Now()), nil)
	elapsed := time.Since(start)

	if res.Err == nil {
		t.Fatal("a run with a slow node past the deadline must fail")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("the deadline must reach the node context: run took %s against a 100ms deadline", elapsed)
	}
}

func TestNodeBudget_AbortsWhenExceeded(t *testing.T) {
	// Ten independent root nodes, budget of 3: the 4th dispatch must abort.
	g := &graph.Graph{}
	for i := 0; i < 10; i++ {
		g.Nodes = append(g.Nodes, graph.Node{ID: graph.NodeID(rune('a' + i)), Type: "const"})
	}
	factory := func(n graph.Node) (Executable, error) { return constNode(n.ID), nil }

	bounds := defaultBounds()
	bounds.NodeExecutions = 3
	bounds.ConcurrentBranches = 1 // deterministic dispatch order for this assertion

	res := Run(context.Background(), g, factory, fakeMeta{}, bounds, NewRunContext("r", time.Now()), nil)
	if res.Err != ErrNodeBudgetExceeded {
		t.Fatalf("expected ErrNodeBudgetExceeded, got %v", res.Err)
	}
}

// --- terminal / Emit Status semantics ---

type statusMeta struct{ fakeMeta }

func (statusMeta) Outputs(nodeType string) []graph.PortName {
	if nodeType == "status" {
		return []graph.PortName{"out"}
	}
	return fakeMeta{}.Outputs(nodeType)
}

func TestNoTerminalNode_YieldsUnknownWithWarning(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{{ID: "a", Type: "const"}}}
	factory := func(n graph.Node) (Executable, error) { return constNode(1), nil }

	res := Run(context.Background(), g, factory, fakeMeta{}, defaultBounds(), NewRunContext("r", time.Now()), nil)
	if res.Status != frame.StatusUnknown {
		t.Fatalf("expected StatusUnknown, got %s", res.Status)
	}
	if res.Warning == "" {
		t.Fatal("a run reaching no Emit Status node must carry an execution warning")
	}
}

func TestTerminalNode_ReportsItsStatus(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{{ID: "s", Type: "status"}}}
	factory := func(n graph.Node) (Executable, error) {
		return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
			return Outputs{"out": frame.Frame{Type: types.Status(), Value: frame.StatusDown}}, nil
		}), nil
	}
	res := Run(context.Background(), g, factory, statusMeta{}, defaultBounds(), NewRunContext("r", time.Now()), nil)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.Status != frame.StatusDown {
		t.Fatalf("expected StatusDown from the terminal node, got %s", res.Status)
	}
	if res.Warning != "" {
		t.Fatalf("a run that reaches a terminal node should carry no warning, got %q", res.Warning)
	}
}

// --- concurrency sanity: independent branches actually run without waiting
// on each other ---

func TestIndependentBranches_RunConcurrently(t *testing.T) {
	g := &graph.Graph{
		Nodes: []graph.Node{{ID: "x", Type: "slow"}, {ID: "y", Type: "slow"}},
	}
	const delay = 150 * time.Millisecond
	factory := func(n graph.Node) (Executable, error) {
		return fakeExec(func(ctx context.Context, rc *RunContext, in Inputs) (Outputs, error) {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return Outputs{"out": frame.Frame{Type: types.Any(), Value: n.ID}}, nil
		}), nil
	}
	bounds := defaultBounds()
	bounds.ConcurrentBranches = 8

	start := time.Now()
	res := Run(context.Background(), g, factory, fakeMeta{}, bounds, NewRunContext("r", time.Now()), nil)
	elapsed := time.Since(start)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if elapsed > delay+250*time.Millisecond {
		t.Fatalf("two independent %s nodes took %s: not running concurrently", delay, elapsed)
	}
}
