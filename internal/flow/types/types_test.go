package types

import "testing"

func TestCheck(t *testing.T) {
	cases := []struct {
		name        string
		src, dst    Type
		allowed     bool
		wantSuggest string
	}{
		{"exact bytes", Bytes(), Bytes(), true, ""},
		// I1: transports emit bytes and decoding is always explicit. This refusal,
		// with its suggestion, is the single most common thing an author will see.
		{"bytes into string", Bytes(), String(), false, "parse.decode"},
		{"string into number", String(), Number(), false, "parse.coerce"},
		{"number into duration", Number(), Duration(), false, "transform.unit_convert"},
		{"number into status", Number(), Status(), false, "emit.threshold"},
		{"list into scalar", List(Record()), Record(), false, "control.foreach"},
		{"scalar into list", Record(), List(Record()), false, "control.collect"},
		{"list of matching elems", List(Number()), List(Number()), true, ""},
		{"record widens to json", Record(), JSON(), true, ""},
		{"int widens to number", Int(), Number(), true, ""},
		{"any source", Any(), Status(), true, ""},
		{"void to non-void", Void(), Bytes(), false, ""},
		{"error to non-error", Error(), String(), false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Check(c.src, c.dst)
			if v.Allowed != c.allowed {
				t.Fatalf("Check(%s, %s).Allowed = %v, want %v (reason: %q)", c.src, c.dst, v.Allowed, c.allowed, v.Reason)
			}
			if v.Suggest != c.wantSuggest {
				t.Errorf("Check(%s, %s).Suggest = %q, want %q", c.src, c.dst, v.Suggest, c.wantSuggest)
			}
			if !v.Allowed && v.Reason == "" {
				t.Error("a refusal must always carry a reason")
			}
		})
	}
}

func TestIntWideningWarns(t *testing.T) {
	// int exists precisely because 64-bit counters and OIDs overflow a float,
	// so widening it must never be silent.
	if v := Check(Int(), Number()); v.Warning == "" {
		t.Fatal("int → number must warn about precision loss")
	}
}
