package executor

import (
	"github.com/qubered/beacon/internal/flow/graph"
)

// MergeVars applies the precedence in spec §6.2: **monitor vars override
// device vars override flow defaults.**
//
// This ordering is what lets one flow serve fourteen devices. The flow author
// writes a sensible default; the device carries what is true of that box (a
// channel count, an address offset); the monitor overrides for the one case
// that is special. Reversing any pair would mean the more specific value loses
// to the more general one, and the symptom is a monitor that silently uses the
// wrong channel count — a wrong answer rather than an error.
//
// The result is a fresh map. Merging into the device's or the monitor's own map
// would mutate configuration that other runs share, and the corruption would
// appear on an unrelated monitor.
func MergeVars(g *graph.Graph, d Device, m Monitor) map[string]any {
	out := make(map[string]any, len(d.Vars)+len(m.Vars)+4)

	// 1. Flow defaults, the most general.
	for k, v := range flowDefaults(g) {
		out[k] = v
	}
	// 2. Device vars.
	for k, v := range d.Vars {
		out[k] = v
	}
	// 3. Monitor vars, the most specific, applied last so they win.
	for k, v := range m.Vars {
		out[k] = v
	}

	// Device identity is not a var an author sets; it is context every flow
	// can read. Applied after the merge so a var named "device.host" cannot
	// misdirect a transport to somewhere the operator never configured.
	out["device.id"] = d.ID
	out["device.name"] = d.Name
	out["device.host"] = d.Host
	out["device.tags"] = d.Tags
	out["monitor.id"] = m.ID
	out["monitor.name"] = m.Name
	out["monitor.interval_ms"] = m.Interval.Milliseconds()

	return out
}

// flowDefaults reads the flow-level defaults from the graph.
//
// Returns nil when the graph declares none, which is the common case: most
// flows take everything from the device.
func flowDefaults(g *graph.Graph) map[string]any {
	if g == nil {
		return nil
	}
	return g.Defaults
}
