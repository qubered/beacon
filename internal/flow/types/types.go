package types

import "fmt"

// Kind enumerates the frame types from spec §6.1.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindBytes        // Raw octets. Everything I/O produces this (invariant I1).
	KindString       // Text — only after an explicit decode.
	KindNumber       // Floating point.
	KindInt          // Arbitrary-precision integer, for 64-bit counters and OIDs that overflow a float.
	KindBool
	KindJSON      // Parsed structure.
	KindRecord    // Named fields.
	KindList      // Ordered collection; Elem is set.
	KindDuration  // Distinct from number so unit mistakes are caught at edit time.
	KindTimestamp // Likewise.
	KindStatus    // up / degraded / down / unknown.
	KindError     // Only on error ports.
	KindAny       // Escape hatch. The editor warns; the runtime does not coerce.
	KindVoid      // Sequencing-only edges.
)

var kindNames = map[Kind]string{
	KindBytes: "bytes", KindString: "string", KindNumber: "number", KindInt: "int",
	KindBool: "bool", KindJSON: "json", KindRecord: "record", KindList: "list",
	KindDuration: "duration", KindTimestamp: "timestamp", KindStatus: "status",
	KindError: "error", KindAny: "any", KindVoid: "void",
}

// Type is a frame type. Elem is populated only for KindList.
type Type struct {
	Kind Kind
	Elem *Type
}

func Bytes() Type     { return Type{Kind: KindBytes} }
func String() Type    { return Type{Kind: KindString} }
func Number() Type    { return Type{Kind: KindNumber} }
func Int() Type       { return Type{Kind: KindInt} }
func Bool() Type      { return Type{Kind: KindBool} }
func JSON() Type      { return Type{Kind: KindJSON} }
func Record() Type    { return Type{Kind: KindRecord} }
func Duration() Type  { return Type{Kind: KindDuration} }
func Timestamp() Type { return Type{Kind: KindTimestamp} }
func Status() Type    { return Type{Kind: KindStatus} }
func Error() Type     { return Type{Kind: KindError} }
func Any() Type       { return Type{Kind: KindAny} }
func Void() Type      { return Type{Kind: KindVoid} }

// List returns list<elem>.
func List(elem Type) Type { return Type{Kind: KindList, Elem: &elem} }

func (t Type) String() string {
	if t.Kind == KindList {
		if t.Elem == nil {
			return "list<any>"
		}
		return "list<" + t.Elem.String() + ">"
	}
	if n, ok := kindNames[t.Kind]; ok {
		return n
	}
	return fmt.Sprintf("kind(%d)", t.Kind)
}

func (t Type) Equal(o Type) bool {
	if t.Kind != o.Kind {
		return false
	}
	if t.Kind != KindList {
		return true
	}
	switch {
	case t.Elem == nil && o.Elem == nil:
		return true
	case t.Elem == nil || o.Elem == nil:
		return false
	default:
		return t.Elem.Equal(*o.Elem)
	}
}

// Verdict is the result of edit-time connection validation (spec §6.1).
//
// A refusal always carries a Reason and, where one exists, a Suggest naming the
// node that fixes it. That suggestion is what makes typed ports feel like help
// rather than obstruction; without it they are merely an obstacle.
type Verdict struct {
	Allowed bool
	Warning string // non-empty when allowed but lossy or unchecked
	Reason  string // non-empty when refused
	Suggest string // node type that would bridge the gap, if any
}

func ok() Verdict           { return Verdict{Allowed: true} }
func warn(w string) Verdict { return Verdict{Allowed: true, Warning: w} }
func no(reason, suggest string) Verdict {
	return Verdict{Reason: reason, Suggest: suggest}
}

// bridges maps a refused (source, destination) kind pair to the node that fixes it.
var bridges = map[[2]Kind]struct{ node, how string }{
	{KindBytes, KindString}:     {"byteops.decode", "insert Decode (utf-8) to connect bytes → string"},
	{KindBytes, KindJSON}:       {"byteops.decode", "insert Decode then Parse (json) to connect bytes → json"},
	{KindBytes, KindNumber}:     {"byteops.slice", "insert Slice then Coerce, or Bit Field, to read a number out of bytes"},
	{KindBytes, KindInt}:        {"byteops.slice", "insert Slice with an integer width and endianness to read an int out of bytes"},
	{KindString, KindBytes}:     {"byteops.encode", "insert Encode (utf-8) to connect string → bytes"},
	{KindString, KindNumber}:    {"parse.coerce", "insert Coerce (number) — conversion is never implicit"},
	{KindString, KindInt}:       {"parse.coerce", "insert Coerce (int) — conversion is never implicit"},
	{KindString, KindBool}:      {"parse.coerce", "insert Coerce (bool) — conversion is never implicit"},
	{KindString, KindTimestamp}: {"parse.coerce", "insert Coerce (timestamp) with an explicit format"},
	{KindString, KindJSON}:      {"parse.parse", "insert Parse (json) to connect string → json"},
	{KindString, KindRecord}:    {"parse.regex_extract", "insert Regex Extract with named capture groups to produce a record"},
	{KindJSON, KindRecord}:      {"parse.jsonpath", "insert JSONPath to select fields into a record"},
	{KindNumber, KindDuration}:  {"transform.unit_convert", "insert Unit Convert — duration is a distinct type so unit mistakes are caught here"},
	{KindNumber, KindTimestamp}: {"transform.unit_convert", "insert Unit Convert — timestamp is a distinct type so unit mistakes are caught here"},
	{KindNumber, KindStatus}:    {"emit.threshold", "insert Threshold to turn a number into a status"},
	{KindRecord, KindStatus}:    {"emit.assert", "insert Assert to turn record fields into a status"},
}

// Check validates a proposed edge from a source port of type src to a
// destination port of type dst.
//
// The rules, in order (spec §6.1): exact match; void sequencing; any on either
// side with a warning; a small set of safe widenings; everything else refused.
func Check(src, dst Type) Verdict {
	if src.Kind == KindInvalid || dst.Kind == KindInvalid {
		return no("port type is not set", "")
	}

	// void is sequencing only, in both directions.
	if src.Kind == KindVoid || dst.Kind == KindVoid {
		if src.Kind == dst.Kind {
			return ok()
		}
		return no("void edges carry no value and can only connect to other void ports", "")
	}

	if src.Equal(dst) {
		return ok()
	}

	// error frames only travel on error ports, or into an any port.
	if src.Kind == KindError && dst.Kind != KindAny {
		return no("an error frame can only be wired into an error port or an any port", "")
	}

	if src.Kind == KindAny {
		return warn("source is any: the runtime does not coerce, so a mismatch here fails at run time rather than edit time")
	}
	if dst.Kind == KindAny {
		return warn("destination is any: downstream type checking stops here")
	}

	// Safe widenings.
	switch {
	case src.Kind == KindInt && dst.Kind == KindNumber:
		return warn("int → number loses precision above 2^53; keep it as int if this is a 64-bit counter or an OID")
	case src.Kind == KindRecord && dst.Kind == KindJSON:
		return ok()
	case src.Kind == KindList && dst.Kind == KindList:
		if dst.Elem == nil || dst.Elem.Kind == KindAny {
			return warn("element type checking stops at list<any>")
		}
		if src.Elem == nil || src.Elem.Kind == KindAny {
			return warn("source element type is any: element mismatches fail at run time")
		}
		inner := Check(*src.Elem, *dst.Elem)
		if inner.Allowed {
			return inner
		}
		return no("list element types do not match: "+inner.Reason, inner.Suggest)
	}

	if src.Kind == KindList && dst.Kind != KindList {
		return no("a list cannot feed a single value; iterate it",
			"control.foreach")
	}
	if src.Kind != KindList && dst.Kind == KindList {
		return no("a single value cannot feed a list input; collect it",
			"control.collect")
	}

	if b, found := bridges[[2]Kind{src.Kind, dst.Kind}]; found {
		return no(b.how, b.node)
	}
	return no(fmt.Sprintf("cannot connect %s → %s", src, dst), "")
}
