package emit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

func assertOf(t *testing.T, rows ...AssertRow) runtime.Executable {
	t.Helper()
	b, _ := json.Marshal(AssertConfig{Rows: rows})
	n, err := newAssert(graph.Node{Type: "emit.assert", Config: b})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAssert_AllPassEmitsUp(t *testing.T) {
	n := assertOf(t, AssertRow{Field: "value", Operator: "lt", Value: 200.0})
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{
		"in": {Value: frame.Record{"value": "128"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["out"].Value != frame.StatusUp {
		t.Fatalf("expected StatusUp, got %v", out["out"].Value)
	}
}

func TestAssert_FailureFiresAssertionClass(t *testing.T) {
	n := assertOf(t, AssertRow{Field: "value", Operator: "lt", Value: 100.0, Message: "battery low"})
	_, err := n.Execute(context.Background(), nil, runtime.Inputs{
		"in": {Value: frame.Record{"value": "128"}},
	})
	fail, ok := err.(frame.Failure)
	if !ok {
		t.Fatalf("expected frame.Failure, got %T", err)
	}
	if fail.Class != frame.ClassAssertion {
		t.Fatalf("expected ClassAssertion (the device answered, value out of range), got %s", fail.Class)
	}
	if fail.Message != "battery low" {
		t.Fatalf("expected the configured message, got %q", fail.Message)
	}
}

func TestAssert_ExistsOperator(t *testing.T) {
	n := assertOf(t, AssertRow{Field: "nonce", Operator: "exists"})
	if _, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: frame.Record{"nonce": "x"}}}); err != nil {
		t.Fatalf("field present: expected pass, got %v", err)
	}
	if _, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: frame.Record{}}}); err == nil {
		t.Fatal("field absent: expected assertion failure")
	}
}

func TestAssert_RejectsUnsupportedOperatorAtConstruction(t *testing.T) {
	if _, err := assertOfErr(AssertRow{Field: "x", Operator: "between"}); err == nil {
		t.Fatal("expected an error for an unsupported operator")
	}
}

func assertOfErr(rows ...AssertRow) (runtime.Executable, error) {
	b, _ := json.Marshal(AssertConfig{Rows: rows})
	return newAssert(graph.Node{Type: "emit.assert", Config: b})
}
