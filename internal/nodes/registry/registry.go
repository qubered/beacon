package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/types"
)

// Tier is the palette difficulty tier (spec §6.4). Tiers 2 and 3 are filtered
// out of the palette by default: most users never leave tiers 1–2, and showing
// someone CRC configuration on day one is how you lose them.
type Tier uint8

const (
	Tier1 Tier = 1 // ping, HTTP, assert, threshold, status
	Tier2 Tier = 2 // connection scope, regex, lookup, foreach
	Tier3 Tier = 3 // build bytes, CRC, bit fields, sandbox
)

// Port is one input or output on a node.
type Port struct {
	Name     string
	Type     types.Type
	Variadic bool // fan-in is refused except on ports marked variadic (spec §6.1)
	Optional bool
}

// Descriptor declares one node type in the catalogue.
type Descriptor struct {
	// Type is the stable identifier, "family.name" — for example
	// "transport.tcp_request". It appears in flow graphs and in agent capability
	// sets, so it is part of the wire contract and must not change.
	Type string

	Title    string
	Summary  string
	Category string
	Tier     Tier

	// Synonyms drive palette search (spec §15.5): "telnet", "ascii", "port 23"
	// must all surface the raw-ASCII TCP preset.
	Synonyms []string

	// ConfigSchemaVersion is bumped whenever the node's configuration shape
	// changes incompatibly. Core refuses to assign a monitor to an agent whose
	// declared version for this node type is lower (spec §7.5).
	ConfigSchemaVersion int

	Inputs  []Port
	Outputs []Port

	// Terminal marks a node that ends a branch (Emit Status).
	Terminal bool

	// WriteCapable marks a node that mutates a device. Off by default per
	// installation, individually enabled by an admin, locally enabled per agent,
	// visually flagged in the editor, and audit-logged per execution (spec §16).
	WriteCapable bool

	// PerformsEgress marks a node that opens a connection outward, so the
	// agent's locally authoritative egress policy applies (invariant I7).
	PerformsEgress bool

	// SessionCapable marks a node usable as a session-mode frame source (spec §9).
	SessionCapable bool

	// New constructs an Executable for one graph node of this type. It is the
	// execution half of what registering a descriptor means — Outputs/Inputs/
	// Terminal above are the metadata half, consumed by graph validation and
	// capability declaration without needing this at all.
	New runtime.Factory
}

// Registry is the node catalogue. An agent's declared capability set is derived
// from its registry, so capability gating is a property of the build rather than
// a hand-maintained list.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Descriptor
}

func New() *Registry { return &Registry{items: make(map[string]Descriptor)} }

// Default is the process-wide catalogue. Node packages register into it from
// their init functions.
var Default = New()

func (r *Registry) Register(d Descriptor) error {
	if d.Type == "" {
		return fmt.Errorf("node descriptor has no Type")
	}
	if d.ConfigSchemaVersion < 1 {
		return fmt.Errorf("node %s: ConfigSchemaVersion must be >= 1", d.Type)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.items[d.Type]; dup {
		return fmt.Errorf("node %s registered twice", d.Type)
	}
	r.items[d.Type] = d
	return nil
}

// MustRegister is for use in init functions.
func MustRegister(d Descriptor) {
	if err := Default.Register(d); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(typ string) (Descriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[typ]
	return d, ok
}

func (r *Registry) All() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.items))
	for _, d := range r.items {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Capabilities is the node-type half of an agent's capability set: node type to
// implemented config schema version (spec §7.5).
type Capabilities map[string]int

func (r *Registry) Capabilities() Capabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	caps := make(Capabilities, len(r.items))
	for t, d := range r.items {
		caps[t] = d.ConfigSchemaVersion
	}
	return caps
}

// Missing reports which of the node types a flow requires are absent from, or
// too old in, the given capability set.
//
// Core calls this before assignment so a gap surfaces in the UI as
// "3 monitors cannot be assigned to agent lx: it does not implement Checksum",
// rather than failing at 6pm.
func Missing(required map[string]int, have Capabilities) []string {
	var missing []string
	for typ, wantVersion := range required {
		gotVersion, present := have[typ]
		if !present || gotVersion < wantVersion {
			missing = append(missing, typ)
		}
	}
	sort.Strings(missing)
	return missing
}
