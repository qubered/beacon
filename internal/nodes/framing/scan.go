package framing

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
)

// ErrIncomplete is what a scanner returns when the buffer holds the start of a
// frame but not all of it. The reader answers by reading more; at EOF it
// becomes a protocol failure, because a device that closed mid-frame gave us
// something we cannot honestly parse.
var errIncomplete = fmt.Errorf("incomplete frame")

// scan finds the next complete frame in buf.
//
// It returns how many bytes of buf the frame consumed (which may exceed the
// frame's own length, since a delimiter can be consumed but excluded) and the
// frame payload itself. errIncomplete means "read more"; any other error is a
// protocol failure.
//
// atEOF changes the answer for exactly the strategies where it should: until
// close and quiet period are *defined* by the stream ending or going silent,
// while a length prefix that is short at EOF is a truncated message and an
// error.
func scan(s Strategy, buf []byte, atEOF bool) (advance int, out []byte, err error) {
	switch s.Kind {
	case KindDelimiter:
		i := bytes.Index(buf, s.Delimiter)
		if i < 0 {
			return 0, nil, errIncomplete
		}
		end := i + len(s.Delimiter)
		if s.IncludeDelimiter {
			return end, buf[:end], nil
		}
		return end, buf[:i], nil

	case KindLengthPrefix:
		header := s.Offset + s.Width
		if len(buf) < header {
			return 0, nil, errIncomplete
		}
		declared, err := readUint(buf[s.Offset:header], s.Endian)
		if err != nil {
			return 0, nil, err
		}
		if declared > uint64(s.MaxDeclared) {
			// One corrupt byte in a length field otherwise makes the reader
			// wait for a message the device will never send. Failing here
			// gives the flow author "declared 4294967295, bound is 65536",
			// which points straight at the offset or endianness being wrong.
			return 0, nil, fmt.Errorf("declared length %d exceeds the configured bound %d — check the prefix offset, width and endianness", declared, s.MaxDeclared)
		}
		total := header + int(declared)
		if s.LengthCovers {
			total = int(declared)
			if total < header {
				return 0, nil, fmt.Errorf("declared length %d is shorter than the %d-byte header it covers", declared, header)
			}
		}
		if len(buf) < total {
			return 0, nil, errIncomplete
		}
		return total, buf[:total], nil

	case KindFixed:
		if len(buf) < s.Size {
			return 0, nil, errIncomplete
		}
		return s.Size, buf[:s.Size], nil

	case KindRegex:
		re, err := compile(s.Pattern)
		if err != nil {
			return 0, nil, err
		}
		loc := re.FindIndex(buf)
		if loc == nil {
			return 0, nil, errIncomplete
		}
		// The match ends the read (spec §6.4), so the frame is everything up
		// to and including it.
		return loc[1], buf[:loc[1]], nil

	case KindMarkers:
		return scanMarkers(s, buf)

	case KindUntilClose, KindQuietPeriod:
		// Both are defined by the stream stopping rather than by content: the
		// reader decides when that has happened (FIN for until-close, a silent
		// interval for quiet period) and only then calls scan with atEOF.
		if !atEOF {
			return 0, nil, errIncomplete
		}
		return len(buf), buf, nil

	case KindMessageCount:
		total := 0
		for i := 0; i < s.Count; i++ {
			adv, _, err := scan(*s.Inner, buf[total:], atEOF)
			if err != nil {
				return 0, nil, err
			}
			total += adv
		}
		return total, buf[:total], nil

	default:
		return 0, nil, fmt.Errorf("unknown read-until strategy %q", s.Kind)
	}
}

// scanMarkers finds a start marker, then the matching end marker, honouring an
// escape byte if one is configured.
func scanMarkers(s Strategy, buf []byte) (int, []byte, error) {
	start := bytes.Index(buf, s.Start)
	if start < 0 {
		return 0, nil, errIncomplete
	}
	from := start + len(s.Start)
	for i := from; i+len(s.End) <= len(buf); i++ {
		if s.Escape != 0 && buf[i] == s.Escape {
			i++ // the next byte is literal, including an end marker's first byte
			continue
		}
		if bytes.HasPrefix(buf[i:], s.End) {
			end := i + len(s.End)
			return end, buf[start:end], nil
		}
	}
	return 0, nil, errIncomplete
}

func readUint(b []byte, e Endianness) (uint64, error) {
	le := e == LittleEndian
	switch len(b) {
	case 1:
		return uint64(b[0]), nil
	case 2:
		if le {
			return uint64(binary.LittleEndian.Uint16(b)), nil
		}
		return uint64(binary.BigEndian.Uint16(b)), nil
	case 4:
		if le {
			return uint64(binary.LittleEndian.Uint32(b)), nil
		}
		return uint64(binary.BigEndian.Uint32(b)), nil
	case 8:
		if le {
			return binary.LittleEndian.Uint64(b), nil
		}
		return binary.BigEndian.Uint64(b), nil
	default:
		return 0, fmt.Errorf("length prefix width must be 1, 2, 4 or 8 bytes (got %d)", len(b))
	}
}

// compile builds the regex used by regex framing.
//
// Go's regexp is RE2: no backtracking, linear in input size, so a pattern a
// flow author pasted from a forum cannot pin a CPU (spec §16). The same
// property is why Regex Extract must use this engine and why the editor's live
// match preview calls the API rather than running a second engine in the
// browser — a backtracking engine in the preview would agree on every pattern
// except the ones that matter.
func compile(pattern string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return re, nil
}
