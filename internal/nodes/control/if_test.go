package control

import (
	"context"
	"testing"

	"github.com/qubered/beacon/internal/engine/runtime"
)

func TestIf_FiresExactlyOneBranch(t *testing.T) {
	n := ifNode{}

	out, err := n.Execute(context.Background(), nil, runtime.Inputs{"cond": {Value: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["true"]; !ok {
		t.Fatal("expected the true port to fire")
	}
	if _, ok := out["false"]; ok {
		t.Fatal("the untaken branch must not appear in Outputs at all")
	}

	out, err = n.Execute(context.Background(), nil, runtime.Inputs{"cond": {Value: false}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["false"]; !ok {
		t.Fatal("expected the false port to fire")
	}
	if _, ok := out["true"]; ok {
		t.Fatal("the untaken branch must not appear in Outputs at all")
	}
}

func TestIf_RejectsNonBoolCond(t *testing.T) {
	n := ifNode{}
	if _, err := n.Execute(context.Background(), nil, runtime.Inputs{"cond": {Value: "yes"}}); err == nil {
		t.Fatal("expected an error for a non-bool cond")
	}
}
