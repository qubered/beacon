package capture

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

// sealedMarker is what a capture shows in place of a sealed frame's value. It
// deliberately carries no information about the value's shape or length,
// since even a length can leak (spec §16: masking is by value, not by field
// name — but a marker that varied in size would itself be a side channel).
const sealedMarker = "••• sealed •••"

// Rendered is a capture-safe representation of one frame: JSON-serialisable,
// truncated to the configured limit, and masked if the frame was sealed.
type Rendered struct {
	Type      string `json:"type"`
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
	Sealed    bool   `json:"sealed,omitempty"`
}

// Render converts a frame into its capture-safe form.
//
// Sealing is checked first and unconditionally: invariant I4 requires a secret
// never be written to a run capture, so a sealed frame is masked before any
// other processing touches its value, regardless of type or size.
func Render(f frame.Frame, maxBytes int64) Rendered {
	if f.Sealed {
		return Rendered{Type: f.Type.String(), Value: sealedMarker, Sealed: true}
	}

	switch v := f.Value.(type) {
	case []byte:
		if int64(len(v)) > maxBytes {
			return Rendered{Type: f.Type.String(), Value: v[:maxBytes], Truncated: true}
		}
		return Rendered{Type: f.Type.String(), Value: v}
	case string:
		if int64(len(v)) > maxBytes {
			return Rendered{Type: f.Type.String(), Value: v[:maxBytes], Truncated: true}
		}
		return Rendered{Type: f.Type.String(), Value: v}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return Rendered{Type: f.Type.String(), Value: nil, Truncated: true}
		}
		if int64(len(b)) > maxBytes {
			return Rendered{Type: f.Type.String(), Value: json.RawMessage(b[:maxBytes]), Truncated: true}
		}
		return Rendered{Type: f.Type.String(), Value: v}
	}
}

// Entry is one node's captured contribution to a run.
type Entry struct {
	Node    graph.NodeID                `json:"node"`
	Inputs  map[graph.PortName]Rendered `json:"inputs,omitempty"`
	Outputs map[graph.PortName]Rendered `json:"outputs,omitempty"`
	Failure *frame.Failure              `json:"failure,omitempty"`
	Skipped bool                        `json:"skipped,omitempty"`
	Started time.Time                   `json:"started,omitempty"`
	Ended   time.Time                   `json:"ended,omitempty"`
}

// Recorder accumulates Entries for one run and satisfies runtime.Capturer.
//
// Retention policy — the last N runs per monitor, plus every run in a suspect
// window (spec §8) — is a property of what happens to a Recorder's Entries
// after the run, in internal/core/store; this type only captures, it does not
// decide what survives.
type Recorder struct {
	maxCapturedFrameSize int64

	mu      sync.Mutex
	entries []Entry
}

func NewRecorder(maxCapturedFrameSize int64) *Recorder {
	return &Recorder{maxCapturedFrameSize: maxCapturedFrameSize}
}

// Compile-time check that Recorder satisfies runtime.Capturer. Inputs and
// Outputs are defined types, not aliases, so a Record method built against
// plain map[...]... parameters silently fails to satisfy the interface until
// something actually tries to use it that way — this line is what catches
// that at build time instead.
var _ runtime.Capturer = (*Recorder)(nil)

// Record implements runtime.Capturer.
func (r *Recorder) Record(node graph.NodeID, in runtime.Inputs, out runtime.Outputs, failure *frame.Failure, skipped bool, started, ended time.Time) {
	e := Entry{Node: node, Failure: failure, Skipped: skipped, Started: started, Ended: ended}
	if len(in) > 0 {
		e.Inputs = make(map[graph.PortName]Rendered, len(in))
		for port, f := range in {
			e.Inputs[port] = Render(f, r.maxCapturedFrameSize)
		}
	}
	if len(out) > 0 {
		e.Outputs = make(map[graph.PortName]Rendered, len(out))
		for port, f := range out {
			e.Outputs[port] = Render(f, r.maxCapturedFrameSize)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// Entries returns a snapshot of everything recorded so far, in the order
// nodes finished.
func (r *Recorder) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}
