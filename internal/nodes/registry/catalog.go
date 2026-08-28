package registry

import (
	"fmt"

	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
)

// PortTypes adapts the registry to graph.PortTypeFunc, so Graph.Validate can
// type-check edges against real registered node types instead of a fake test
// table.
func (r *Registry) PortTypes() graph.PortTypeFunc {
	return func(nodeType string, port graph.PortName, output bool) (types.Type, bool) {
		if port == "error" && output {
			return types.Error(), true
		}
		d, ok := r.Get(nodeType)
		if !ok {
			return types.Type{}, false
		}
		ports := d.Inputs
		if output {
			ports = d.Outputs
		}
		for _, p := range ports {
			if p.Name == string(port) {
				return p.Type, true
			}
		}
		return types.Type{}, false
	}
}

// portMeta adapts the registry to runtime.PortMeta, so the engine can drive
// firing and skip cascades from real descriptors.
type portMeta struct{ r *Registry }

func (m portMeta) Outputs(nodeType string) []graph.PortName {
	d, ok := m.r.Get(nodeType)
	if !ok {
		return nil
	}
	out := make([]graph.PortName, 0, len(d.Outputs))
	for _, p := range d.Outputs {
		out = append(out, graph.PortName(p.Name))
	}
	return out
}

func (m portMeta) Required(nodeType string, port graph.PortName) bool {
	d, ok := m.r.Get(nodeType)
	if !ok {
		// An unknown (nodeType, port) pair should have been caught at publish
		// time by Graph.Validate against PortTypes above. Treating it as
		// required here means a gap surfaces as a skipped-then-failed node
		// rather than the engine silently proceeding without an input nobody
		// declared.
		return true
	}
	for _, p := range d.Inputs {
		if p.Name == string(port) {
			return !p.Optional
		}
	}
	return true
}

func (m portMeta) Terminal(nodeType string) bool {
	d, ok := m.r.Get(nodeType)
	return ok && d.Terminal
}

// PortMeta returns the runtime.PortMeta view of this registry.
func (r *Registry) PortMeta() runtime.PortMeta { return portMeta{r} }

var _ runtime.PortMeta = portMeta{}

// Factory returns the runtime.Factory view of this registry: construct an
// Executable for a graph node by looking up its registered constructor.
func (r *Registry) Factory() runtime.Factory {
	return func(n graph.Node) (runtime.Executable, error) {
		d, ok := r.Get(n.Type)
		if !ok {
			return nil, fmt.Errorf("unknown node type %q", n.Type)
		}
		if d.New == nil {
			return nil, fmt.Errorf("node type %q is registered but has no constructor", n.Type)
		}
		return d.New(n)
	}
}
