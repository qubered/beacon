package framing

// Telnet command bytes (RFC 854/855). Named here rather than inline because a
// bare 0xFB in a state machine is unreadable six months later.
const (
	iac  = 0xFF // Interpret As Command
	sb   = 0xFA // Subnegotiation begin
	se   = 0xF0 // Subnegotiation end
	will = 0xFB
	wont = 0xFC
	do   = 0xFD
	dont = 0xFE
)

type iacState uint8

const (
	iacGround    iacState = iota // ordinary data
	iacSawIAC                    // read 0xFF, deciding what follows
	iacSawVerb                   // read WILL/WONT/DO/DONT, one option byte follows
	iacInSub                     // inside a subnegotiation
	iacSubSawIAC                 // inside a subnegotiation, read 0xFF
)

// iacStripper removes Telnet command sequences from a byte stream.
//
// It is stateful because a sequence can straddle a read boundary: a device
// that sends IAC as the last byte of one TCP segment and WILL ECHO at the
// start of the next is not doing anything unusual, and a stripper that
// examined each read in isolation would pass the WILL through as data. That
// stray byte then lands in the middle of a response and breaks a parse in a
// way that looks like a device fault.
//
// This is a filter, not a Telnet client. Options are discarded, never
// answered — the port-23 AV devices this exists for send negotiation at
// connect and neither expect nor understand a reply (decision D8).
type iacStripper struct {
	state iacState
}

func (s *iacStripper) filter(in []byte) []byte {
	out := in[:0:0] // never alias the caller's buffer
	for _, b := range in {
		switch s.state {
		case iacGround:
			if b == iac {
				s.state = iacSawIAC
				continue
			}
			out = append(out, b)

		case iacSawIAC:
			switch b {
			case iac:
				// IAC IAC is a literal 0xFF, which is real data — the one
				// escape that must survive stripping, or every binary
				// protocol tunnelled over port 23 loses its 0xFF bytes.
				out = append(out, iac)
				s.state = iacGround
			case will, wont, do, dont:
				s.state = iacSawVerb
			case sb:
				s.state = iacInSub
			default:
				// A two-byte command. Drop it.
				s.state = iacGround
			}

		case iacSawVerb:
			// The option byte. Drop it and return to data.
			s.state = iacGround

		case iacInSub:
			if b == iac {
				s.state = iacSubSawIAC
			}

		case iacSubSawIAC:
			if b == se {
				s.state = iacGround
			} else {
				// IAC IAC inside a subnegotiation, or a malformed sequence.
				// Either way we are still inside it.
				s.state = iacInSub
			}
		}
	}
	return out
}
