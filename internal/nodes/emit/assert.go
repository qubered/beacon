package emit

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// AssertRow is one check: spec §6.4 — "field, operator, value, severity,
// message template". Severity and Message are carried for the alert layer
// (M5) and the run capture; M1's engine only needs pass/fail.
type AssertRow struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
}

// AssertConfig configures emit.assert.
//
// This is a subset of the operator list in spec §6.4 (equality, ordering,
// containment, existence, regex match) — enough to prove the node without
// building the full set. Ranges, prefix/suffix, set membership, and type/schema
// checks land in M8 alongside the rest of the catalogue.
type AssertConfig struct {
	Rows []AssertRow `json:"rows"`
}

type assertNode struct{ cfg AssertConfig }

func newAssert(n graph.Node) (runtime.Executable, error) {
	var cfg AssertConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("emit.assert: invalid config: %w", err)
		}
	}
	if len(cfg.Rows) == 0 {
		return nil, fmt.Errorf("emit.assert: at least one row is required")
	}
	for _, row := range cfg.Rows {
		if row.Field == "" {
			return nil, fmt.Errorf("emit.assert: a row is missing its field")
		}
		if !supportedOperator(row.Operator) {
			return nil, fmt.Errorf("emit.assert: unsupported operator %q", row.Operator)
		}
	}
	return &assertNode{cfg: cfg}, nil
}

func supportedOperator(op string) bool {
	switch op {
	case "eq", "ne", "lt", "lte", "gt", "gte", "contains", "regex", "exists":
		return true
	}
	return false
}

func (a *assertNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	inFrame, ok := in["in"]
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "no input on port \"in\"")
	}
	rec, ok := inFrame.Value.(frame.Record)
	if !ok {
		return nil, frame.Fail(frame.ClassInternal, "input frame is not a record (got %T)", inFrame.Value)
	}

	for _, row := range a.cfg.Rows {
		val, present := rec[row.Field]
		ok, err := evalRow(row, val, present)
		if err != nil {
			return nil, frame.Fail(frame.ClassInternal, "assert %s %s: %v", row.Field, row.Operator, err)
		}
		if !ok {
			msg := row.Message
			if msg == "" {
				msg = fmt.Sprintf("assertion failed: %s %s %v (got %v)", row.Field, row.Operator, row.Value, val)
			}
			// A failed assertion means the device answered and the value is
			// out of range — spec §11's ClassAssertion, routed to the AV
			// on-call as the real signal, not to the flow author.
			return nil, frame.Fail(frame.ClassAssertion, "%s", msg)
		}
	}

	// Assert bridges record -> status directly (see the {Record,Status} entry
	// in flow/types' suggestion table): passing every row means the thing
	// being checked is up. A failed row does not itself decide "down" — it
	// fires the error port instead, and it is the flow author's wiring (an
	// Emit Status downstream of that port) that decides what an assertion
	// failure means for this particular check.
	return runtime.Outputs{"out": inFrame.Derive(types.Status(), frame.StatusUp)}, nil
}

func evalRow(row AssertRow, val any, present bool) (bool, error) {
	if row.Operator == "exists" {
		return present, nil
	}
	if !present {
		return false, nil
	}

	switch row.Operator {
	case "eq":
		return fmt.Sprint(val) == fmt.Sprint(row.Value), nil
	case "ne":
		return fmt.Sprint(val) != fmt.Sprint(row.Value), nil
	case "contains":
		return strings.Contains(fmt.Sprint(val), fmt.Sprint(row.Value)), nil
	case "regex":
		pattern, ok := row.Value.(string)
		if !ok {
			return false, fmt.Errorf("regex operator needs a string pattern")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("invalid pattern: %w", err)
		}
		return re.MatchString(fmt.Sprint(val)), nil
	case "lt", "lte", "gt", "gte":
		got, ok := toFloat(val)
		if !ok {
			return false, fmt.Errorf("%v is not numeric", val)
		}
		want, ok := toFloat(row.Value)
		if !ok {
			return false, fmt.Errorf("comparison value %v is not numeric", row.Value)
		}
		switch row.Operator {
		case "lt":
			return got < want, nil
		case "lte":
			return got <= want, nil
		case "gt":
			return got > want, nil
		default:
			return got >= want, nil
		}
	}
	return false, fmt.Errorf("unsupported operator %q", row.Operator)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "emit.assert",
		Title:               "Assert",
		Summary:             "Check record fields against rules with no expression required.",
		Category:            "Evaluate and emit",
		Tier:                registry.Tier1,
		Synonyms:            []string{"assert", "check", "validate", "rule"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "in", Type: types.Record()}},
		Outputs:             []registry.Port{{Name: "out", Type: types.Status()}},
		New:                 newAssert,
	})
}
