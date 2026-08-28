package control

import (
	"context"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// ifNode is control.if: predicate in, exactly one of true/false out (spec
// §6.4). Firing only one output port is what the engine's skip cascade keys
// off of — the untaken branch's edges resolve to skipped, and a join
// downstream of both branches fires on whichever one actually settled (the
// branch-join rule in internal/engine/runtime).
//
// The predicate is a plain bool input for M1. Spec §6.5's tier-1 expression
// language, which is what a real flow would use to compute one, arrives in M8;
// until then a predicate has to come from another node's output (e.g. a
// Coerce or Assert once wired) or a session frame handler's derived state.
type ifNode struct{}

func (ifNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	condFrame, ok := in["cond"]
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "no input on port \"cond\"")
	}
	cond, ok := condFrame.Value.(bool)
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "cond frame is not a bool (got %T)", condFrame.Value)
	}
	if cond {
		return runtime.Outputs{"true": condFrame.Derive(types.Void(), nil)}, nil
	}
	return runtime.Outputs{"false": condFrame.Derive(types.Void(), nil)}, nil
}

func newIf(n graph.Node) (runtime.Executable, error) { return ifNode{}, nil }

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "control.if",
		Title:               "If",
		Summary:             "Branch on a predicate. Exactly one of true/false fires.",
		Category:            "Control flow",
		Tier:                registry.Tier1,
		Synonyms:            []string{"if", "branch", "condition", "predicate"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "cond", Type: types.Bool()}},
		Outputs: []registry.Port{
			{Name: "true", Type: types.Void()},
			{Name: "false", Type: types.Void()},
		},
		New: newIf,
	})
}
