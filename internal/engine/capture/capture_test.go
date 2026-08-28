package capture

import (
	"testing"
	"time"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
)

func TestRender_MasksSealedRegardlessOfType(t *testing.T) {
	// I4: a secret must never be written to a run capture. This must hold even
	// when the sealed value is composed into something large — masking is by
	// the frame's Sealed flag, not by inspecting the value's shape.
	f := frame.Frame{Type: types.String(), Value: "s3cr3t-in-a-bigger-payload", Sealed: true}
	r := Render(f, 1024)
	if !r.Sealed {
		t.Fatal("Sealed must be true on the rendered entry")
	}
	if r.Value == f.Value {
		t.Fatal("a sealed frame's actual value must never appear in the capture")
	}
	if s, ok := r.Value.(string); !ok || s == "" {
		t.Fatal("the sealed marker itself should render as a fixed placeholder")
	}
}

func TestRender_TruncatesOversizedBytes(t *testing.T) {
	big := make([]byte, 100)
	for i := range big {
		big[i] = byte(i)
	}
	f := frame.Frame{Type: types.Bytes(), Value: big}
	r := Render(f, 10)
	if !r.Truncated {
		t.Fatal("expected Truncated to be set")
	}
	got, ok := r.Value.([]byte)
	if !ok || len(got) != 10 {
		t.Fatalf("expected value truncated to 10 bytes, got %v", r.Value)
	}
}

func TestRender_SmallValuesPassThroughUntruncated(t *testing.T) {
	f := frame.Frame{Type: types.String(), Value: "up"}
	r := Render(f, 1024)
	if r.Truncated || r.Sealed {
		t.Fatalf("small unsealed value should render plainly, got %+v", r)
	}
	if r.Value != "up" {
		t.Fatalf("expected value to pass through, got %v", r.Value)
	}
}

func TestRecorder_AccumulatesInOrder(t *testing.T) {
	rec := NewRecorder(1024)
	rec.Record(graph.NodeID("a"), nil, nil, nil, true, time.Time{}, time.Time{})
	rec.Record(graph.NodeID("b"), nil, nil, nil, false, time.Now(), time.Now())

	entries := rec.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Node != "a" || !entries[0].Skipped {
		t.Fatalf("expected first entry to be the skipped node a, got %+v", entries[0])
	}
	if entries[1].Node != "b" || entries[1].Skipped {
		t.Fatalf("expected second entry to be the non-skipped node b, got %+v", entries[1])
	}
}

func TestRecorder_FailureIsCaptured(t *testing.T) {
	rec := NewRecorder(1024)
	f := frame.Fail(frame.ClassTimeout, "no reply")
	rec.Record(graph.NodeID("n"), nil, nil, &f, false, time.Now(), time.Now())
	entries := rec.Entries()
	if entries[0].Failure == nil || entries[0].Failure.Class != frame.ClassTimeout {
		t.Fatalf("expected the failure to be captured, got %+v", entries[0])
	}
}
