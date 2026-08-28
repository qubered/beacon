// Package acceptance holds cross-package proofs — this file is not one of the
// five §18 scenarios (those need transports, which arrive in M2) but it is the
// same idea one size down: prove the real stack end to end, not each piece in
// isolation.
package acceptance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/nodes/registry"

	// Registered via init() side effects — this is exactly what an agent's
	// main package does to assemble its catalogue.
	_ "github.com/qubered/beacon/internal/nodes/byteops"
	_ "github.com/qubered/beacon/internal/nodes/control"
	_ "github.com/qubered/beacon/internal/nodes/emit"
	_ "github.com/qubered/beacon/internal/nodes/parse"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wirelessReceiverGraph builds a real flow using only the five nodes M1
// ships: decode the raw bytes a device sent, extract the battery run-time
// field, assert it's in range, and emit a status. This is a shrunk-down
// preview of acceptance scenario A (spec §18) — the full version needs
// Connection Scope and session mode, which land in M7.
//
// "decode" has no incoming edge: it is the run's root, and its bytes come
// from a fixture rather than a transport, exactly the way beaconctl's
// `flow run --fixture` supplies them until M2 ships real transports.
func wirelessReceiverGraph(t *testing.T, threshold float64) *graph.Graph {
	t.Helper()
	return &graph.Graph{
		Nodes: []graph.Node{
			{ID: "decode", Type: "byteops.decode"},
			{ID: "extract", Type: "parse.regex_extract", Config: mustJSON(t, map[string]any{
				"pattern": `< GET (?P<channel>\d) BATT_RUN_TIME (?P<value>\d+) >`,
			})},
			{ID: "check", Type: "emit.assert", Config: mustJSON(t, map[string]any{
				"rows": []map[string]any{
					{"field": "value", "operator": "lt", "value": threshold, "message": "battery run time critically low"},
				},
			})},
			{ID: "status", Type: "emit.emit_status"},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "decode", Port: "out"}, To: graph.Endpoint{Node: "extract", Port: "in"}},
			{From: graph.Endpoint{Node: "extract", Port: "out"}, To: graph.Endpoint{Node: "check", Port: "in"}},
			{From: graph.Endpoint{Node: "check", Port: "out"}, To: graph.Endpoint{Node: "status", Port: "in"}},
		},
	}
}

// fixtureFactory wraps the registry's real factory so a named root node's
// "in" input is synthesised from a literal byte fixture instead of a wire —
// standing in for what beaconctl's `flow run --fixture` does for real.
func fixtureFactory(reg *registry.Registry, rootID graph.NodeID, fixture []byte) runtime.Factory {
	real := reg.Factory()
	return func(n graph.Node) (runtime.Executable, error) {
		exec, err := real(n)
		if err != nil {
			return nil, err
		}
		if n.ID != rootID {
			return exec, nil
		}
		return fixtureWrap{inner: exec, fixture: fixture}, nil
	}
}

type fixtureWrap struct {
	inner   runtime.Executable
	fixture []byte
}

func (w fixtureWrap) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	if in == nil {
		in = runtime.Inputs{}
	}
	in["in"] = frame.Frame{Value: w.fixture}
	return w.inner.Execute(ctx, rc, in)
}

func TestM1Pipeline_ValidatesAndExecutes(t *testing.T) {
	reg := registry.Default // the five real nodes register into this via init()
	g := wirelessReceiverGraph(t, 200)

	if err := g.Validate(reg.PortTypes()); err != nil {
		t.Fatalf("a real bytes->string->record->status pipeline must validate cleanly: %v", err)
	}

	factory := fixtureFactory(reg, "decode", []byte("< GET 1 BATT_RUN_TIME 128 >"))
	rc := runtime.NewRunContext("run-1", time.Now())
	res := runtime.Run(context.Background(), g, factory, reg.PortMeta(), config.DefaultBounds(), rc, nil)

	if res.Err != nil {
		t.Fatalf("unexpected run error: %v", res.Err)
	}
	if res.Status != frame.StatusUp {
		t.Fatalf("battery value 128 < 200 threshold should assert clean and report up, got %s (warning: %s)", res.Status, res.Warning)
	}
	if res.Warning != "" {
		t.Fatalf("a run that reaches Emit Status should carry no warning, got %q", res.Warning)
	}
	for _, id := range []graph.NodeID{"decode", "extract", "check", "status"} {
		if res.Nodes[id].State != runtime.NodeDone {
			t.Errorf("node %s: expected done, got %s", id, res.Nodes[id].State)
		}
	}
}

func TestM1Pipeline_UnconnectedErrorPortFailsWithRealNodes(t *testing.T) {
	reg := registry.Default
	// Same flow, but "check"'s threshold (100) is below the fixture's battery
	// value (128), so the assertion fails. "status" sits downstream of check's
	// success port only — check's error port is left unconnected, exactly the
	// case spec §6.2 describes: "Unconnected, an error propagates and fails
	// the run with full context captured."
	g := wirelessReceiverGraph(t, 100)

	if err := g.Validate(reg.PortTypes()); err != nil {
		t.Fatalf("graph must validate: %v", err)
	}

	factory := fixtureFactory(reg, "decode", []byte("< GET 1 BATT_RUN_TIME 128 >"))
	rc := runtime.NewRunContext("run-2", time.Now())
	res := runtime.Run(context.Background(), g, factory, reg.PortMeta(), config.DefaultBounds(), rc, nil)

	if res.Err == nil {
		t.Fatal("an assertion failure with no wired error port must fail the run")
	}
	uf, ok := res.Err.(*runtime.UnhandledFailure)
	if !ok {
		t.Fatalf("expected *runtime.UnhandledFailure, got %T: %v", res.Err, res.Err)
	}
	if uf.Failure.Class != frame.ClassAssertion {
		t.Fatalf("expected ClassAssertion, got %s", uf.Failure.Class)
	}
	if res.Status != frame.StatusUnknown {
		t.Fatalf("a run that failed outright reports unknown, not a guessed status; got %s", res.Status)
	}
	if res.Nodes["status"].State != runtime.NodeSkipped {
		t.Fatalf("status sits behind check's success port only, so it must be skipped, got %s", res.Nodes["status"].State)
	}
}
