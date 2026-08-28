package emit

import (
	"context"
	"testing"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

func TestEmitStatus_PassesThroughValidStatus(t *testing.T) {
	n, err := newEmitStatus(graph.Node{Type: "emit.emit_status"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: frame.StatusDown}})
	if err != nil {
		t.Fatal(err)
	}
	if out["out"].Value != frame.StatusDown {
		t.Fatalf("expected StatusDown, got %v", out["out"].Value)
	}
}

func TestEmitStatus_RejectsInvalidStatusValue(t *testing.T) {
	n, _ := newEmitStatus(graph.Node{Type: "emit.emit_status"})
	if _, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: frame.Status("sideways")}}); err == nil {
		t.Fatal("expected an error for an invalid status value")
	}
}
