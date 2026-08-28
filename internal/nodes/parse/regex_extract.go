package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// RegexExtractConfig configures parse.regex_extract.
type RegexExtractConfig struct {
	// Pattern must use named capture groups: (?P<name>...). Unnamed groups
	// are matched but not exposed on the output record.
	Pattern string `json:"pattern"`
}

type regexExtractNode struct {
	cfg RegexExtractConfig
	re  *regexp.Regexp
}

func newRegexExtract(n graph.Node) (runtime.Executable, error) {
	var cfg RegexExtractConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("parse.regex_extract: invalid config: %w", err)
		}
	}
	if cfg.Pattern == "" {
		return nil, fmt.Errorf("parse.regex_extract: pattern is required")
	}
	// Go's regexp is RE2: non-backtracking, guaranteed linear in input size
	// (spec §6.4, §16). This is also why decision D29 chose Go — the editor's
	// live match preview has to agree with this exact engine, on exactly the
	// patterns where a backtracking engine would differ.
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, fmt.Errorf("parse.regex_extract: invalid pattern: %w", err)
	}
	return &regexExtractNode{cfg: cfg, re: re}, nil
}

func (n *regexExtractNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	inFrame, ok := in["in"]
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "no input on port \"in\"")
	}
	s, ok := inFrame.Value.(string)
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "input frame is not a string (got %T)", inFrame.Value)
	}

	match := n.re.FindStringSubmatch(s)
	if match == nil {
		// A non-match is the device saying something the flow author did not
		// expect — that is a protocol-class failure (spec §11), not an
		// internal one: the device answered and we misread it.
		return nil, frame.Fail(frame.ClassProtocol, "pattern did not match")
	}

	rec := frame.Record{}
	for i, name := range n.re.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		rec[name] = match[i]
	}

	return runtime.Outputs{"out": inFrame.Derive(types.Record(), rec)}, nil
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "parse.regex_extract",
		Title:               "Regex Extract",
		Summary:             "Extract named fields from text with a regular expression.",
		Category:            "Parse and extract",
		Tier:                registry.Tier2,
		Synonyms:            []string{"regex", "regexp", "extract", "capture group", "pattern"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "in", Type: types.String()}},
		Outputs:             []registry.Port{{Name: "out", Type: types.Record()}},
		New:                 newRegexExtract,
	})
}
