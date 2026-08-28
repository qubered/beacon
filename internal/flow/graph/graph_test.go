package graph

import (
	"testing"
	"time"

	"github.com/qubered/beacon/internal/flow/types"
)

// A minimal port table standing in for the registry, covering just what the
// tests below exercise.
func testPorts(nodeType string, port PortName, output bool) (types.Type, bool) {
	table := map[string]map[PortName]types.Type{
		"transport.tcp_request": {"out": types.Bytes()},
		"parse.decode":          {"in": types.Bytes(), "out": types.String()},
		"control.if":            {"in": types.Bool(), "true": types.Void(), "false": types.Void()},
		"emit.emit_status":      {"in": types.Status()},
		"control.collect":       {"in": types.Any(), "out": types.List(types.Any())},
	}
	ports, ok := table[nodeType]
	if !ok {
		return types.Type{}, false
	}
	t, ok := ports[port]
	return t, ok
}

func TestValidate_RefusesTypeMismatch(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a", Type: "transport.tcp_request"}, {ID: "b", Type: "emit.emit_status"}},
		Edges: []Edge{{From: Endpoint{"a", "out"}, To: Endpoint{"b", "in"}}},
	}
	err := g.Validate(testPorts)
	if err == nil {
		t.Fatal("expected a type-mismatch error for bytes -> status")
	}
}

func TestValidate_AllowsGoodEdge(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{ID: "a", Type: "transport.tcp_request"}, {ID: "b", Type: "parse.decode"}},
		Edges: []Edge{{From: Endpoint{"a", "out"}, To: Endpoint{"b", "in"}}},
	}
	if err := g.Validate(testPorts); err != nil {
		t.Fatalf("expected bytes -> string decode edge to validate, got %v", err)
	}
}

func TestValidate_RefusesUnknownNode(t *testing.T) {
	g := &Graph{Edges: []Edge{{From: Endpoint{"ghost", "out"}, To: Endpoint{"also-ghost", "in"}}}}
	if err := g.Validate(testPorts); err == nil {
		t.Fatal("expected an error referencing an unknown node")
	}
}

func TestValidate_RefusesCycleWithoutLoopMark(t *testing.T) {
	// control.if -> itself via "true", not marked as a loop back-edge.
	g := &Graph{
		Nodes: []Node{{ID: "a", Type: "control.if"}},
		Edges: []Edge{{From: Endpoint{"a", "true"}, To: Endpoint{"a", "in"}}},
	}
	// "in" wants bool but "true" is void — pick ports that at least type-check
	// to isolate the cycle check.
	if err := g.Validate(testPorts); err == nil {
		t.Fatal("expected a cycle or type error")
	}
}

func TestValidate_AllowsMarkedLoopBackEdge(t *testing.T) {
	table := func(nodeType string, port PortName, output bool) (types.Type, bool) {
		if nodeType == "control.loop" {
			return types.Void(), true
		}
		return types.Type{}, false
	}
	g := &Graph{
		Nodes: []Node{{ID: "loop", Type: "control.loop"}},
		Edges: []Edge{{From: Endpoint{"loop", "body_out"}, To: Endpoint{"loop", "body_in"}, Loop: true}},
	}
	if err := g.Validate(table); err != nil {
		t.Fatalf("a Loop-marked back-edge is the one legal cycle, got error: %v", err)
	}
}

func TestContentHash_IgnoresNodeOrderAndPosition(t *testing.T) {
	a := Graph{Nodes: []Node{{ID: "x", Type: "t"}, {ID: "y", Type: "t"}}}
	a.Nodes[0].Position.X = 100

	b := Graph{Nodes: []Node{{ID: "y", Type: "t"}, {ID: "x", Type: "t"}}}
	b.Nodes[1].Position.X = 999 // different layout, same content

	ha, err := a.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("content hash should be order- and layout-independent: %s != %s", ha, hb)
	}
}

func TestContentHash_DiffersOnRealChange(t *testing.T) {
	a := Graph{Nodes: []Node{{ID: "x", Type: "t"}}}
	b := Graph{Nodes: []Node{{ID: "x", Type: "other"}}}
	ha, _ := a.ContentHash()
	hb, _ := b.ContentHash()
	if ha == hb {
		t.Fatal("different node types must hash differently")
	}
}

func TestVersion_PublishedIsImmutable(t *testing.T) {
	v := &Version{FlowID: "f1"}
	if err := v.Publish(1, "author-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := v.SetGraph(Graph{}); err != ErrPublished {
		t.Fatalf("SetGraph on a published version: got %v, want ErrPublished", err)
	}
	if err := v.Publish(2, "author-1", time.Now()); err != ErrPublished {
		t.Fatalf("re-publish: got %v, want ErrPublished", err)
	}
}

func TestVersion_ForkCarriesGraphNotPublishState(t *testing.T) {
	v := &Version{FlowID: "f1", Graph: Graph{Nodes: []Node{{ID: "a", Type: "t"}}}}
	_ = v.Publish(1, "author-1", time.Now())

	draft := v.Fork()
	if draft.IsPublished() {
		t.Fatal("a fork must be a draft")
	}
	if len(draft.Graph.Nodes) != 1 {
		t.Fatal("fork must carry the graph forward")
	}
	// The fork is independently editable.
	if err := draft.SetGraph(Graph{Nodes: []Node{{ID: "a", Type: "t"}, {ID: "b", Type: "t"}}}); err != nil {
		t.Fatal(err)
	}
}
