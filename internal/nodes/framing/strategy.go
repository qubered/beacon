package framing

import (
	"fmt"
	"time"
)

// Kind names a read-until strategy (spec §6.4).
type Kind string

const (
	KindDelimiter    Kind = "delimiter"
	KindLengthPrefix Kind = "length_prefix"
	KindFixed        Kind = "fixed"
	KindRegex        Kind = "regex"
	KindMarkers      Kind = "markers"
	KindQuietPeriod  Kind = "quiet_period"
	KindUntilClose   Kind = "until_close"
	KindMessageCount Kind = "message_count"
)

// Endianness for a length prefix.
type Endianness string

const (
	BigEndian    Endianness = "big"
	LittleEndian Endianness = "little"
)

// Strategy is one read-until configuration. It is the single shape used by TCP
// Request's read, Expect inside a Connection Scope, and Split Frames over
// bytes already in hand — one engine, three call sites, so a protocol that
// works in one works in all three.
type Strategy struct {
	Kind Kind `json:"kind"`

	// Delimiter
	Delimiter        []byte `json:"delimiter,omitempty"`
	IncludeDelimiter bool   `json:"include_delimiter,omitempty"`

	// LengthPrefix
	Offset       int        `json:"offset,omitempty"`
	Width        int        `json:"width,omitempty"`
	Endian       Endianness `json:"endian,omitempty"`
	LengthCovers bool       `json:"length_covers_header,omitempty"`
	MaxDeclared  int        `json:"max_declared,omitempty"`

	// Fixed
	Size int `json:"size,omitempty"`

	// Regex — matched against a decoded view of the buffer; the match ends the
	// read.
	Pattern string `json:"pattern,omitempty"`

	// Markers
	Start  []byte `json:"start,omitempty"`
	End    []byte `json:"end,omitempty"`
	Escape byte   `json:"escape,omitempty"`

	// QuietPeriod — the universal fallback for prompt-less devices.
	Quiet time.Duration `json:"quiet,omitempty"`

	// MessageCount reads Count messages framed by Inner.
	Count int       `json:"count,omitempty"`
	Inner *Strategy `json:"inner,omitempty"`
}

// Options are the per-read modifiers that apply to every strategy (spec §6.4).
type Options struct {
	// StripIAC removes Telnet IAC sequences. Mandatory for the very common
	// port-23-but-not-really-Telnet class of device, which sends option
	// negotiation bytes at connect and nothing resembling a prompt after.
	StripIAC bool `json:"strip_iac,omitempty"`

	// MaxBytes caps one read. Zero means the caller's frame-size bound
	// applies instead.
	MaxBytes int `json:"max_bytes,omitempty"`

	// DiscardBefore skips a banner: everything up to and including the first
	// match of this marker is dropped before framing begins.
	DiscardBefore []byte `json:"discard_before,omitempty"`
}

// Validate reports whether a strategy is configurable as written. It runs at
// edit time so a misconfigured frame is refused at a desk rather than
// producing a hang in front of a console — and every message names what to
// change, per the repository's refusal-carries-a-suggestion rule.
func (s Strategy) Validate() error {
	switch s.Kind {
	case KindDelimiter:
		if len(s.Delimiter) == 0 {
			return fmt.Errorf("delimiter framing needs a delimiter — set one, or use quiet period for a device with no terminator")
		}
	case KindLengthPrefix:
		switch s.Width {
		case 1, 2, 4, 8:
		default:
			return fmt.Errorf("length prefix width must be 1, 2, 4 or 8 bytes (got %d)", s.Width)
		}
		if s.Offset < 0 {
			return fmt.Errorf("length prefix offset cannot be negative (got %d)", s.Offset)
		}
		if s.Endian != BigEndian && s.Endian != LittleEndian {
			return fmt.Errorf("length prefix needs an endianness of %q or %q", BigEndian, LittleEndian)
		}
		if s.MaxDeclared <= 0 {
			return fmt.Errorf("length prefix needs a sanity bound: set max_declared, or one corrupt byte makes the reader wait for 4GB")
		}
	case KindFixed:
		if s.Size <= 0 {
			return fmt.Errorf("fixed framing needs a positive size (got %d)", s.Size)
		}
	case KindRegex:
		if s.Pattern == "" {
			return fmt.Errorf("regex framing needs a pattern")
		}
		re, err := compile(s.Pattern)
		if err != nil {
			return err
		}
		if re.MatchString("") {
			// A pattern that matches the empty string matches before a single
			// byte has been read, so the read "succeeds" with nothing — the
			// device is never heard from and a dead device is indistinguishable
			// from an empty response. Patterns like `.*` and `\d*` are easy to
			// write and this is not a failure anyone would diagnose from the
			// symptom, so it is refused where it can still be fixed.
			return fmt.Errorf("regex pattern %q matches the empty string, so it would match before any bytes arrive — anchor it or require at least one character (for example %s instead of a `*` quantifier)", s.Pattern, "`+`")
		}
	case KindMarkers:
		if len(s.Start) == 0 || len(s.End) == 0 {
			return fmt.Errorf("start/end marker framing needs both a start and an end marker")
		}
	case KindQuietPeriod:
		if s.Quiet <= 0 {
			return fmt.Errorf("quiet period framing needs a positive duration")
		}
	case KindUntilClose:
		// Nothing to configure. Read to FIN.
	case KindMessageCount:
		if s.Count <= 0 {
			return fmt.Errorf("message count framing needs a positive count (got %d)", s.Count)
		}
		if s.Inner == nil {
			return fmt.Errorf("message count framing needs an inner strategy describing how one message is framed")
		}
		if s.Inner.Kind == KindMessageCount {
			return fmt.Errorf("message count framing cannot nest inside itself")
		}
		return s.Inner.Validate()
	default:
		return fmt.Errorf("unknown read-until strategy %q", s.Kind)
	}
	return nil
}
