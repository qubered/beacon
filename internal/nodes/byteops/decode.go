package byteops

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// DecodeConfig configures byteops.decode.
type DecodeConfig struct {
	// Encoding is one of utf-8, ascii, latin-1, hex, base64. Empty means utf-8.
	//
	// BCD, gzip and deflate are in the spec's encoding list (§6.4) and are not
	// implemented yet — they arrive with M8's byte-node sweep. Requesting one
	// here is a construction-time error, not a silent pass-through, because a
	// silently wrong decode of binary framing is exactly the kind of surprise
	// principle 2 exists to rule out.
	Encoding string `json:"encoding"`
}

type decodeNode struct{ cfg DecodeConfig }

func newDecode(n graph.Node) (runtime.Executable, error) {
	var cfg DecodeConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("byteops.decode: invalid config: %w", err)
		}
	}
	switch cfg.Encoding {
	case "", "utf-8", "ascii", "latin-1", "hex", "base64":
	default:
		return nil, fmt.Errorf("byteops.decode: unsupported encoding %q", cfg.Encoding)
	}
	return &decodeNode{cfg: cfg}, nil
}

func (d *decodeNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	inFrame, ok := in["in"]
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "no input on port \"in\"")
	}
	b, ok := inFrame.Value.([]byte)
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "input frame is not bytes (got %T)", inFrame.Value)
	}

	var s string
	switch d.cfg.Encoding {
	case "", "utf-8", "ascii", "latin-1":
		// Bytes-first, principle 2: this does not validate, repair or reject
		// invalid sequences. It exposes the bytes as Go's native string
		// representation and nothing more. A device that violates its own
		// declared encoding is a fact worth seeing in the byte inspector, not
		// a fact worth silently correcting.
		s = string(b)
	case "hex":
		s = hex.EncodeToString(b)
	case "base64":
		s = base64.StdEncoding.EncodeToString(b)
	}

	return runtime.Outputs{"out": inFrame.Derive(types.String(), s)}, nil
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "byteops.decode",
		Title:               "Decode",
		Summary:             "Decode bytes to text under an explicit encoding.",
		Category:            "Bytes and encoding",
		Tier:                registry.Tier1,
		Synonyms:            []string{"decode", "utf-8", "ascii", "hex", "base64", "text"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "in", Type: types.Bytes()}},
		Outputs:             []registry.Port{{Name: "out", Type: types.String()}},
		New:                 newDecode,
	})
}
