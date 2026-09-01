package framing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrTruncated is returned when a read hit its byte cap before the strategy
// found a frame boundary. It is distinct from a timeout because the causes are
// different: a truncation means the framing is wrong or the cap is too low,
// while a timeout means the device did not answer.
var ErrTruncated = errors.New("read exceeded its byte cap before the frame ended")

// defaultReadChunk is the buffer handed to one Read call. It bounds nothing —
// MaxBytes does that — it just avoids a syscall per byte.
const defaultReadChunk = 4096

// Read reads one frame from r according to s and o.
//
// The connection is read directly rather than through a bufio.Reader on
// purpose. Buffering would let a read consume bytes belonging to the *next*
// frame, which is invisible in a single-shot request but silently corrupts a
// multi-turn Connection Scope conversation, where the next Expect then starts
// mid-message. Leftover bytes are instead returned to the caller so a session
// can carry them forward.
//
// ctx carries the run deadline. For a net.Conn the caller has already had an
// abort adapter attached (internal/agent/egress), so a cancelled context
// destroys the socket rather than leaving this loop blocked in Read.
func Read(ctx context.Context, r io.Reader, s Strategy, o Options) (frame []byte, leftover []byte, err error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}

	var (
		stripper  iacStripper
		buf       []byte
		discarded = len(o.DiscardBefore) == 0
		chunk     = make([]byte, defaultReadChunk)
	)

	for {
		// Ask the strategy whether what we already hold is a complete frame,
		// before reading again. A frame that arrived in full in the previous
		// read must not wait on another one that may never come.
		if discarded {
			advance, out, scanErr := scan(s, buf, false)
			switch {
			case scanErr == nil:
				if advance == 0 {
					// A strategy that claims a complete frame while consuming
					// nothing would return an empty frame without ever reading
					// the socket, and the run would report success against a
					// device that was never heard from. Validate refuses the
					// patterns that can cause this; this is the backstop that
					// keeps a future strategy from reintroducing it silently.
					// Split has the same guard, for the same reason.
					return nil, nil, fmt.Errorf("framing matched a zero-length frame before reading anything — the strategy would never consume input")
				}
				return out, buf[advance:], nil
			case !errors.Is(scanErr, errIncomplete):
				return nil, nil, scanErr
			}
		}

		if o.MaxBytes > 0 && len(buf) >= o.MaxBytes {
			return nil, nil, fmt.Errorf("%w: %d bytes", ErrTruncated, len(buf))
		}

		n, readErr := readChunk(ctx, r, chunk, s)

		if n > 0 {
			in := chunk[:n]
			if o.StripIAC {
				in = stripper.filter(in)
			}
			buf = append(buf, in...)
			if !discarded {
				buf, discarded = discardBefore(buf, o.DiscardBefore)
			}
		}

		if readErr != nil {
			return finish(s, buf, discarded, readErr)
		}
	}
}

// Split applies a read-until strategy to bytes already in hand, which is the
// Split Frames node (spec §6.4) — the same engine, no second implementation,
// so a strategy that frames a live stream correctly frames a recorded one
// identically.
func Split(data []byte, s Strategy, o Options) ([][]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if o.StripIAC {
		var stripper iacStripper
		data = stripper.filter(data)
	}
	if len(o.DiscardBefore) > 0 {
		var ok bool
		data, ok = discardBefore(data, o.DiscardBefore)
		if !ok {
			return nil, fmt.Errorf("discard-before marker never appeared in %d bytes", len(data))
		}
	}

	var out [][]byte
	for len(data) > 0 {
		// Bytes in hand are always at EOF: there is no more data coming, so a
		// quiet-period or until-close strategy takes the remainder.
		advance, f, err := scan(s, data, true)
		if errors.Is(err, errIncomplete) {
			return nil, fmt.Errorf("%d trailing bytes do not form a complete frame", len(data))
		}
		if err != nil {
			return nil, err
		}
		if advance == 0 {
			return nil, fmt.Errorf("framing made no progress on %d remaining bytes", len(data))
		}
		out = append(out, f)
		data = data[advance:]
	}
	return out, nil
}

// readChunk performs one read, applying the quiet-period interval as a
// per-read deadline when that is the strategy.
//
// Quiet period is the universal fallback for prompt-less devices, and it is
// the one strategy whose boundary is time rather than content — so it is the
// one place the reader needs to distinguish "the device paused" from "the
// device is done". A read that times out at exactly the quiet interval means
// the latter, and only here does a timeout end a frame successfully.
func readChunk(ctx context.Context, r io.Reader, chunk []byte, s Strategy) (int, error) {
	if s.Kind == KindQuietPeriod {
		if conn, ok := r.(net.Conn); ok {
			quietUntil := time.Now().Add(s.Quiet)
			if runDeadline, ok := ctx.Deadline(); ok && runDeadline.Before(quietUntil) {
				// The run deadline outranks the quiet interval: a device that
				// keeps dribbling bytes must not extend the run past its
				// budget one quiet period at a time.
				quietUntil = runDeadline
			}
			if err := conn.SetReadDeadline(quietUntil); err != nil {
				return 0, err
			}
			defer conn.SetReadDeadline(time.Time{})
		}
	}
	return r.Read(chunk)
}

// finish decides what a read error means for the strategy in play.
func finish(s Strategy, buf []byte, discarded bool, readErr error) ([]byte, []byte, error) {
	quietSatisfied := s.Kind == KindQuietPeriod && isTimeout(readErr) && len(buf) > 0
	closed := errors.Is(readErr, io.EOF)

	if !discarded {
		return nil, nil, fmt.Errorf("discard-before marker never appeared before the stream ended")
	}
	if !quietSatisfied && !closed {
		return nil, nil, readErr
	}

	advance, out, scanErr := scan(s, buf, true)
	if scanErr == nil {
		return out, buf[advance:], nil
	}
	if errors.Is(scanErr, errIncomplete) {
		if closed {
			// The device closed mid-frame. This is a protocol failure and not
			// a timeout: naming it correctly is what routes it to the flow
			// author rather than the on-call (spec §11).
			return nil, nil, fmt.Errorf("connection closed after %d bytes without completing a frame", len(buf))
		}
		return nil, nil, readErr
	}
	return nil, nil, scanErr
}

// discardBefore drops everything up to and including the first occurrence of
// marker, which is how a login banner is skipped before framing begins.
func discardBefore(buf, marker []byte) ([]byte, bool) {
	i := bytes.Index(buf, marker)
	if i < 0 {
		return buf, false
	}
	return buf[i+len(marker):], true
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
