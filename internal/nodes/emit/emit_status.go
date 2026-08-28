package emit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// EmitStatusConfig configures emit.emit_status. Reason is a plain string for
// M1 — the {{ }} interpolation in spec §6.2 arrives with the expression
// language in M8.
type EmitStatusConfig struct {
	Reason string `json:"reason,omitempty"`
}

type emitStatusNode struct{ cfg EmitStatusConfig }

func newEmitStatus(n graph.Node) (runtime.Executable, error) {
	var cfg EmitStatusConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("emit.emit_status: invalid config: %w", err)
		}
	}
	return &emitStatusNode{cfg: cfg}, nil
}

func (e *emitStatusNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	inFrame, ok := in["in"]
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "no input on port \"in\"")
	}
	s, ok := inFrame.Value.(frame.Status)
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "input frame is not a status (got %T)", inFrame.Value)
	}
	if !s.Valid() {
		return nil, frame.Fail(frame.ClassInternal, "invalid status value %q", s)
	}

	// emit.emit_status is terminal (spec §6.2: "A flow must reach one"). Its
	// declared output exists so the run inspector and the engine's own
	// terminal-status scan (internal/engine/runtime.finalizeStatus) have
	// something to read — nothing is expected to wire from it.
	return runtime.Outputs{"out": inFrame.Derive(types.Status(), s)}, nil
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "emit.emit_status",
		Title:               "Emit Status",
		Summary:             "Set the monitor's status. Terminal — every flow must reach one.",
		Category:            "Evaluate and emit",
		Tier:                registry.Tier1,
		Synonyms:            []string{"status", "up", "down", "degraded", "result"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "in", Type: types.Status()}},
		Outputs:             []registry.Port{{Name: "out", Type: types.Status()}},
		Terminal:            true,
		New:                 newEmitStatus,
	})
}
