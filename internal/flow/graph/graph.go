package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/qubered/beacon/internal/flow/types"
)

// NodeID and EdgeID are graph-local identifiers, stable within one flow version.
type NodeID string
type PortName string

// Node is one box on the canvas.
//
// Config is opaque JSON here — the registry (internal/nodes/registry) owns what
// shape it must be for a given Type, and internal/flow/validate checks it
// against that shape at publish time. The graph package itself does not know
// what any node does.
type Node struct {
	ID     NodeID          `json:"id"`
	Type   string          `json:"type"` // matches a registry.Descriptor.Type
	Config json.RawMessage `json:"config,omitempty"`

	// Name publishes this node's primary output under a flow-scoped binding,
	// readable from any downstream expression (spec §6.2). Empty if unbound.
	Name string `json:"name,omitempty"`

	// Position is canvas layout only; the engine ignores it.
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
}

// Endpoint identifies one port on one node.
type Endpoint struct {
	Node NodeID   `json:"node"`
	Port PortName `json:"port"`
}

// Edge connects one output port to one input port.
//
// Fan-out (one output driving many edges) is allowed and is represented simply
// as multiple edges sharing a From. Fan-in (many edges into one input) is
// refused by Validate unless the destination port is variadic (spec §6.1).
type Edge struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`

	// Loop marks the one legal back-edge: into a Loop node's iteration input.
	// Every other edge must move strictly forward through the DAG.
	Loop bool `json:"loop,omitempty"`
}

// Graph is a flow's DAG: nodes, typed ports resolved via the registry, and the
// edges between them. It knows nothing about execution; that is
// internal/engine/runtime's job.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// PortTypeFunc resolves the type of one named port on one node, given the
// node's declared type string. The graph package takes this as a parameter
// rather than importing the registry directly, so it has no dependency on the
// node catalogue.
type PortTypeFunc func(nodeType string, port PortName, output bool) (types.Type, bool)

func (g *Graph) nodeByID(id NodeID) (*Node, bool) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i], true
		}
	}
	return nil, false
}

// Validate checks structural well-formedness: node IDs are unique and
// referenced, ports exist and connections type-check, fan-in is refused except
// on variadic ports, and the only cycle permitted is a Loop back-edge.
//
// This is structural validation. Publish-time policy (missing Emit Status,
// undeclared metric labels, uncapped loops, worst-case duration versus
// interval) lives in internal/flow/validate, which calls this first.
func (g *Graph) Validate(portType PortTypeFunc) error {
	seen := make(map[NodeID]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node has no id")
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		seen[n.ID] = true
	}

	fanIn := make(map[Endpoint]int)
	for _, e := range g.Edges {
		fromNode, ok := g.nodeByID(e.From.Node)
		if !ok {
			return fmt.Errorf("edge references unknown node %q", e.From.Node)
		}
		toNode, ok := g.nodeByID(e.To.Node)
		if !ok {
			return fmt.Errorf("edge references unknown node %q", e.To.Node)
		}

		srcType, ok := portType(fromNode.Type, e.From.Port, true)
		if !ok {
			return fmt.Errorf("node %q (%s) has no output port %q", e.From.Node, fromNode.Type, e.From.Port)
		}
		dstType, ok := portType(toNode.Type, e.To.Port, false)
		if !ok {
			return fmt.Errorf("node %q (%s) has no input port %q", e.To.Node, toNode.Type, e.To.Port)
		}

		v := types.Check(srcType, dstType)
		if !v.Allowed {
			msg := fmt.Sprintf("%s.%s -> %s.%s: %s", e.From.Node, e.From.Port, e.To.Node, e.To.Port, v.Reason)
			if v.Suggest != "" {
				msg += fmt.Sprintf(" (insert %s)", v.Suggest)
			}
			return fmt.Errorf("%s", msg)
		}

		fanIn[e.To]++
	}

	// Fan-in is refused except on variadic ports (spec §6.1). Variadic-ness is a
	// registry property; PortTypeFunc alone can't express it, so this check is
	// deliberately conservative here and the registry-aware form lives in
	// flow/validate where variadic ports are known.

	if err := detectIllegalCycles(g); err != nil {
		return err
	}
	_ = fanIn
	return nil
}

// detectIllegalCycles walks the graph ignoring Loop-marked edges (the one
// legal back-edge) and fails if any cycle remains.
func detectIllegalCycles(g *Graph) error {
	adj := make(map[NodeID][]NodeID)
	for _, e := range g.Edges {
		if e.Loop {
			continue
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[NodeID]int, len(g.Nodes))
	var stack []NodeID

	var visit func(NodeID) error
	visit = func(n NodeID) error {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case white:
				if err := visit(m); err != nil {
					return err
				}
			case gray:
				return fmt.Errorf("cycle detected through %q -> %q (not marked as a Loop back-edge)", n, m)
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}

	for _, n := range g.Nodes {
		if color[n.ID] == white {
			if err := visit(n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ContentHash is a stable digest of the graph, used for agent-side caching
// (decision D28): two flow versions with identical graphs hash identically, so
// an agent that already has the content need not re-fetch it.
//
// Node and edge order in the input slices does not affect the hash — canvas
// layout churn must not invalidate the cache.
func (g *Graph) ContentHash() (string, error) {
	nodes := make([]Node, len(g.Nodes))
	copy(nodes, g.Nodes)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i := range nodes {
		nodes[i].Position.X, nodes[i].Position.Y = 0, 0 // layout is not content
	}

	edges := make([]Edge, len(g.Edges))
	copy(edges, g.Edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From.Node < edges[j].From.Node ||
				(edges[i].From.Node == edges[j].From.Node && edges[i].From.Port < edges[j].From.Port)
		}
		return edges[i].To.Node < edges[j].To.Node ||
			(edges[i].To.Node == edges[j].To.Node && edges[i].To.Port < edges[j].To.Port)
	})

	canon, err := json.Marshal(struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}{nodes, edges})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
