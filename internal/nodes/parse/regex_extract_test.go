package parse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

func node(t *testing.T, pattern string) runtime.Executable {
	t.Helper()
	b, _ := json.Marshal(RegexExtractConfig{Pattern: pattern})
	n, err := newRegexExtract(graph.Node{Type: "parse.regex_extract", Config: b})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRegexExtract_NamedGroupsBecomeRecordFields(t *testing.T) {
	n := node(t, `< GET (?P<channel>\d) BATT_RUN_TIME (?P<value>\d+) >`)
	out, err := n.Execute(context.Background(), nil, runtime.Inputs{
		"in": {Value: "< GET 1 BATT_RUN_TIME 128 >"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := out["out"].Value.(frame.Record)
	if !ok {
		t.Fatalf("expected a record, got %T", out["out"].Value)
	}
	if rec["channel"] != "1" || rec["value"] != "128" {
		t.Fatalf("expected channel=1 value=128, got %v", rec)
	}
}

func TestRegexExtract_NoMatchIsProtocolClass(t *testing.T) {
	n := node(t, `^ACK$`)
	_, err := n.Execute(context.Background(), nil, runtime.Inputs{"in": {Value: "NAK"}})
	fail, ok := err.(frame.Failure)
	if !ok {
		t.Fatalf("expected a frame.Failure, got %T: %v", err, err)
	}
	if fail.Class != frame.ClassProtocol {
		t.Fatalf("a non-match is the device answering unexpectedly, expected ClassProtocol, got %s", fail.Class)
	}
}

func TestRegexExtract_RejectsEmptyPatternAtConstruction(t *testing.T) {
	if _, err := newRegexExtract(graph.Node{Type: "parse.regex_extract", Config: []byte(`{}`)}); err == nil {
		t.Fatal("expected an error for a missing pattern")
	}
}

func TestRegexExtract_RejectsInvalidPatternAtConstruction(t *testing.T) {
	b, _ := json.Marshal(RegexExtractConfig{Pattern: "(unclosed"})
	if _, err := newRegexExtract(graph.Node{Type: "parse.regex_extract", Config: b}); err == nil {
		t.Fatal("expected a compile error for an invalid pattern")
	}
}
