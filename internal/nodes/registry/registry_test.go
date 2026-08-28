package registry

import (
	"context"
	"testing"

	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
)

func TestRegister_RejectsMissingType(t *testing.T) {
	r := New()
	if err := r.Register(Descriptor{ConfigSchemaVersion: 1}); err == nil {
		t.Fatal("expected an error for a descriptor with no Type")
	}
}

func TestRegister_RejectsZeroSchemaVersion(t *testing.T) {
	r := New()
	if err := r.Register(Descriptor{Type: "x.y"}); err == nil {
		t.Fatal("expected an error for ConfigSchemaVersion < 1")
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	r := New()
	d := Descriptor{Type: "x.y", ConfigSchemaVersion: 1}
	if err := r.Register(d); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(d); err == nil {
		t.Fatal("expected an error registering the same type twice")
	}
}

func TestMissing_FlagsAbsentAndStaleCapabilities(t *testing.T) {
	required := map[string]int{"byteops.checksum": 2, "transport.tcp_request": 1}
	have := Capabilities{"transport.tcp_request": 1} // has tcp_request but not checksum at all

	missing := Missing(required, have)
	if len(missing) != 1 || missing[0] != "byteops.checksum" {
		t.Fatalf("expected only byteops.checksum missing, got %v", missing)
	}

	have["byteops.checksum"] = 1 // present but at an older schema version than required
	missing = Missing(required, have)
	if len(missing) != 1 || missing[0] != "byteops.checksum" {
		t.Fatalf("an older config schema version must still count as missing, got %v", missing)
	}
}

func TestPortTypes_ResolvesRealAndImplicitErrorPort(t *testing.T) {
	r := New()
	_ = r.Register(Descriptor{
		Type:                "test.echo",
		ConfigSchemaVersion: 1,
		Inputs:              []Port{{Name: "in", Type: types.Bytes()}},
		Outputs:             []Port{{Name: "out", Type: types.Bytes()}},
	})

	pt := r.PortTypes()
	if typ, ok := pt("test.echo", "in", false); !ok || !typ.Equal(types.Bytes()) {
		t.Fatalf("expected declared input port to resolve, got %v ok=%v", typ, ok)
	}
	if typ, ok := pt("test.echo", "error", true); !ok || !typ.Equal(types.Error()) {
		t.Fatalf("every node type must expose an implicit error output, got %v ok=%v", typ, ok)
	}
	if _, ok := pt("test.echo", "nonexistent", true); ok {
		t.Fatal("an undeclared port must not resolve")
	}
}

func TestPortMeta_RequiredReflectsOptional(t *testing.T) {
	r := New()
	_ = r.Register(Descriptor{
		Type:                "test.node",
		ConfigSchemaVersion: 1,
		Inputs: []Port{
			{Name: "must", Type: types.Any()},
			{Name: "maybe", Type: types.Any(), Optional: true},
		},
		Outputs:  []Port{{Name: "out", Type: types.Any()}},
		Terminal: true,
	})

	pm := r.PortMeta()
	if !pm.Required("test.node", "must") {
		t.Fatal("a non-optional port must be required")
	}
	if pm.Required("test.node", "maybe") {
		t.Fatal("an optional port must not be required")
	}
	if !pm.Terminal("test.node") {
		t.Fatal("Terminal must pass through from the descriptor")
	}
	outs := pm.Outputs("test.node")
	if len(outs) != 1 || outs[0] != "out" {
		t.Fatalf("expected [out], got %v", outs)
	}
}

func TestFactory_ConstructsRegisteredNode(t *testing.T) {
	r := New()
	built := false
	_ = r.Register(Descriptor{
		Type:                "test.thing",
		ConfigSchemaVersion: 1,
		New: func(n graph.Node) (runtime.Executable, error) {
			built = true
			return fakeExecutable{}, nil
		},
	})

	exec, err := r.Factory()(graph.Node{ID: "n1", Type: "test.thing"})
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("expected the registered constructor to run")
	}
	if _, err := exec.Execute(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFactory_RejectsUnknownAndUnconstructableTypes(t *testing.T) {
	r := New()
	if _, err := r.Factory()(graph.Node{Type: "nope"}); err == nil {
		t.Fatal("expected an error for an unregistered node type")
	}
	_ = r.Register(Descriptor{Type: "no.ctor", ConfigSchemaVersion: 1}) // New left nil
	if _, err := r.Factory()(graph.Node{Type: "no.ctor"}); err == nil {
		t.Fatal("expected an error for a descriptor with no constructor")
	}
}

type fakeExecutable struct{}

func (fakeExecutable) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	return runtime.Outputs{}, nil
}
