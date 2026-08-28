package byteops

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

func mustNode(t *testing.T, cfg any) runtime.Executable {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	n, err := newDecode(graph.Node{Type: "byteops.decode", Config: b})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDecode_UTF8(t *testing.T) {
	n := mustNode(t, DecodeConfig{Encoding: "utf-8"})
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{
		"in": {Value: []byte("hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["out"].Value != "hello" {
		t.Fatalf("expected \"hello\", got %v", out["out"].Value)
	}
}

func TestDecode_Hex(t *testing.T) {
	n := mustNode(t, DecodeConfig{Encoding: "hex"})
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: []byte{0xDE, 0xAD, 0xBE, 0xEF}}})
	if err != nil {
		t.Fatal(err)
	}
	if out["out"].Value != "deadbeef" {
		t.Fatalf("expected deadbeef, got %v", out["out"].Value)
	}
}

func TestDecode_RejectsUnsupportedEncodingAtConstruction(t *testing.T) {
	_, err := newDecode(graph.Node{Type: "byteops.decode", Config: []byte(`{"encoding":"gzip"}`)})
	if err == nil {
		t.Fatal("gzip is not implemented yet (M8) and must be rejected at construction, not silently passed through")
	}
}

func TestDecode_RejectsNonBytesInput(t *testing.T) {
	n := mustNode(t, DecodeConfig{})
	_, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: "not bytes"}})
	if err == nil {
		t.Fatal("expected an error for a non-bytes input frame")
	}
}

func TestDecode_PropagatesSeal(t *testing.T) {
	// I4: anything derived from a sealed frame must stay sealed.
	n := mustNode(t, DecodeConfig{})
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{
		"in": {Value: []byte("secret"), Sealed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out["out"].Sealed {
		t.Fatal("decoding a sealed frame must produce a sealed frame")
	}
}
