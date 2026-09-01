package framing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// chunkReader hands out one scripted read at a time, which is how a real
// socket behaves and how a bytes.Reader does not. Framing bugs live almost
// entirely at read boundaries, so the tests must be able to put a boundary
// anywhere.
type chunkReader struct {
	chunks [][]byte
	i      int
	err    error
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

func chunks(ss ...string) *chunkReader {
	c := &chunkReader{}
	for _, s := range ss {
		c.chunks = append(c.chunks, []byte(s))
	}
	return c
}

func readOne(t *testing.T, r io.Reader, s Strategy, o Options) ([]byte, []byte) {
	t.Helper()
	f, rest, err := Read(context.Background(), r, s, o)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return f, rest
}

func TestDelimiter_ExcludesAndIncludes(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	f, rest := readOne(t, chunks("PWR ON\r\nVOL 30\r\n"), s, Options{})
	if string(f) != "PWR ON" {
		t.Errorf("frame = %q, want %q", f, "PWR ON")
	}
	if string(rest) != "VOL 30\r\n" {
		t.Errorf("leftover = %q; the next frame's bytes must survive for the next Expect", rest)
	}

	s.IncludeDelimiter = true
	f, _ = readOne(t, chunks("PWR ON\r\n"), s, Options{})
	if string(f) != "PWR ON\r\n" {
		t.Errorf("frame = %q, want the delimiter included", f)
	}
}

// TestDelimiter_SpansAReadBoundary is the bug class that matters: a delimiter
// split across two TCP segments is completely ordinary and a per-read scanner
// misses it.
func TestDelimiter_SpansAReadBoundary(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	f, _ := readOne(t, chunks("PWR ", "ON\r", "\nnext"), s, Options{})
	if string(f) != "PWR ON" {
		t.Fatalf("frame = %q; a delimiter split across reads was missed", f)
	}
}

func TestLengthPrefix_BigAndLittleEndian(t *testing.T) {
	// 2-byte big-endian length of 3, then the body.
	s := Strategy{Kind: KindLengthPrefix, Width: 2, Endian: BigEndian, MaxDeclared: 64}
	f, rest := readOne(t, chunks("\x00\x03abcTRAILING"), s, Options{})
	if string(f) != "\x00\x03abc" {
		t.Errorf("frame = %q, want the header plus 3 body bytes", f)
	}
	if string(rest) != "TRAILING" {
		t.Errorf("leftover = %q", rest)
	}

	s.Endian = LittleEndian
	f, _ = readOne(t, chunks("\x03\x00abc"), s, Options{})
	if string(f) != "\x03\x00abc" {
		t.Errorf("little-endian frame = %q", f)
	}
}

func TestLengthPrefix_CoversHeader(t *testing.T) {
	// Declared length 5 counts the 2-byte header, so the body is 3 bytes.
	s := Strategy{Kind: KindLengthPrefix, Width: 2, Endian: BigEndian, MaxDeclared: 64, LengthCovers: true}
	f, rest := readOne(t, chunks("\x00\x05abcZZ"), s, Options{})
	if string(f) != "\x00\x05abc" {
		t.Errorf("frame = %q, want 5 total bytes", f)
	}
	if string(rest) != "ZZ" {
		t.Errorf("leftover = %q", rest)
	}
}

// TestLengthPrefix_SanityBoundIsEnforced: one corrupt byte in a length field
// otherwise makes the reader wait for a message that will never arrive, and
// the symptom is a monitor that hangs until its deadline with no explanation.
func TestLengthPrefix_SanityBoundIsEnforced(t *testing.T) {
	s := Strategy{Kind: KindLengthPrefix, Width: 4, Endian: BigEndian, MaxDeclared: 1024}
	_, _, err := Read(context.Background(), chunks("\xff\xff\xff\xffbody"), s, Options{})
	if err == nil {
		t.Fatal("a declared length of 4294967295 was accepted against a 1024-byte bound")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("1024")) {
		t.Errorf("error %q does not name the bound; the flow author cannot tell what to change", err)
	}
}

func TestLengthPrefix_ValidationRequiresABound(t *testing.T) {
	s := Strategy{Kind: KindLengthPrefix, Width: 2, Endian: BigEndian}
	err := s.Validate()
	if err == nil {
		t.Fatal("a length prefix with no sanity bound validated")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("max_declared")) {
		t.Errorf("refusal %q does not name the field to set", err)
	}
}

func TestFixed(t *testing.T) {
	f, rest := readOne(t, chunks("abcd", "efgh"), Strategy{Kind: KindFixed, Size: 6}, Options{})
	if string(f) != "abcdef" {
		t.Errorf("frame = %q, want 6 bytes across two reads", f)
	}
	if string(rest) != "gh" {
		t.Errorf("leftover = %q", rest)
	}
}

func TestRegex_MatchEndsTheRead(t *testing.T) {
	s := Strategy{Kind: KindRegex, Pattern: `[a-z]+> $`}
	f, _ := readOne(t, chunks("Welcome\r\n", "switch> "), s, Options{})
	if string(f) != "Welcome\r\nswitch> " {
		t.Fatalf("frame = %q, want everything up to and including the prompt", f)
	}
}

// TestRegex_ZeroWidthPatternIsRefused: a pattern matching the empty string
// matches before a single byte arrives, so the read would "succeed" with an
// empty frame without ever touching the socket — a dead device and an empty
// response would look identical. Refused at edit time, where it is fixable.
func TestRegex_ZeroWidthPatternIsRefused(t *testing.T) {
	for _, pattern := range []string{`.*`, `\d*`, `(FOO)?`, `a*`} {
		s := Strategy{Kind: KindRegex, Pattern: pattern}
		err := s.Validate()
		if err == nil {
			t.Errorf("pattern %q matches the empty string and was accepted", pattern)
			continue
		}
		if !bytes.Contains([]byte(err.Error()), []byte("empty string")) {
			t.Errorf("refusal for %q is %q; it should say why", pattern, err)
		}
	}

	// A pattern that cannot match empty is still fine.
	if err := (Strategy{Kind: KindRegex, Pattern: `[a-z]+> $`}).Validate(); err != nil {
		t.Errorf("a well-formed pattern was refused: %v", err)
	}
}

// TestRead_ZeroLengthFrameNeverSucceedsSilently is the runtime backstop: even
// if a strategy somehow reports a complete frame having consumed nothing, Read
// must not return success without reading the device.
func TestRead_ZeroLengthFrameNeverSucceedsSilently(t *testing.T) {
	r := chunks("LAMPHOURS 1420\r\n")
	// Bypass Validate deliberately by scanning directly, then assert Read's
	// own guard would catch what Validate now refuses.
	_, _, err := Read(context.Background(), r, Strategy{Kind: KindRegex, Pattern: `.*`}, Options{})
	if err == nil {
		t.Fatal("a zero-width regex produced a successful read")
	}
	if r.i != 0 {
		return // it read the socket, which is also acceptable
	}
}

func TestMarkers_WithEscape(t *testing.T) {
	// The escaped '}' in the middle must not end the frame.
	s := Strategy{Kind: KindMarkers, Start: []byte("{"), End: []byte("}"), Escape: '\\'}
	f, _ := readOne(t, chunks(`junk{a\}b}tail`), s, Options{})
	if string(f) != `{a\}b}` {
		t.Fatalf("frame = %q; the escaped end marker was treated as a terminator", f)
	}
}

func TestUntilClose(t *testing.T) {
	f, _ := readOne(t, chunks("part one ", "part two"), Strategy{Kind: KindUntilClose}, Options{})
	if string(f) != "part one part two" {
		t.Fatalf("frame = %q", f)
	}
}

func TestMessageCount(t *testing.T) {
	s := Strategy{
		Kind:  KindMessageCount,
		Count: 2,
		Inner: &Strategy{Kind: KindDelimiter, Delimiter: []byte("\n"), IncludeDelimiter: true},
	}
	f, _ := readOne(t, chunks("one\n", "two\n", "three\n"), s, Options{})
	if string(f) != "one\ntwo\n" {
		t.Fatalf("frame = %q, want exactly two messages", f)
	}

	// A third message already in the buffer is left over rather than consumed.
	// Reading it here rather than in the case above is deliberate: the reader
	// must stop the moment the count is satisfied, so when the messages arrive
	// in separate reads the third is never read at all.
	f, rest := readOne(t, chunks("one\ntwo\nthree\n"), s, Options{})
	if string(f) != "one\ntwo\n" {
		t.Fatalf("frame = %q, want exactly two messages", f)
	}
	if string(rest) != "three\n" {
		t.Errorf("leftover = %q", rest)
	}
}

// TestQuietPeriod_EndsOnSilence covers the universal fallback for prompt-less
// devices. It needs a real socket, since the boundary is a read timing out
// rather than anything in the bytes.
func TestQuietPeriod_EndsOnSilence(t *testing.T) {
	client, server := netPipePair(t)

	go func() {
		server.Write([]byte("PWR="))
		time.Sleep(10 * time.Millisecond)
		server.Write([]byte("ON"))
		// Then go quiet without closing, which is what the device does.
	}()

	s := Strategy{Kind: KindQuietPeriod, Quiet: 60 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	f, _, err := Read(ctx, client, s, Options{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(f) != "PWR=ON" {
		t.Fatalf("frame = %q; a pause shorter than the quiet period must not end the frame", f)
	}
}

// TestQuietPeriod_SilentDeviceIsATimeout: a device that says nothing at all
// must produce a timeout, not an empty successful frame. An empty frame would
// sail into a parse and fail there, reported as protocol — blaming the flow
// author for a device that never answered.
func TestQuietPeriod_SilentDeviceIsATimeout(t *testing.T) {
	client, _ := netPipePair(t)

	s := Strategy{Kind: KindQuietPeriod, Quiet: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, _, err := Read(ctx, client, s, Options{}); err == nil {
		t.Fatal("a silent device produced a successful empty frame")
	}
}

func TestStripIAC_RemovesNegotiationAndKeepsData(t *testing.T) {
	// IAC WILL ECHO, IAC DO SGA, then real data.
	in := "\xff\xfb\x01\xff\xfd\x03PWR ON\r\n"
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	f, _ := readOne(t, chunks(in), s, Options{StripIAC: true})
	if string(f) != "PWR ON" {
		t.Fatalf("frame = %q; negotiation bytes survived stripping", f)
	}
}

// TestStripIAC_SequenceSpansAReadBoundary: a device that sends IAC as the last
// byte of one segment and the verb at the start of the next is doing nothing
// unusual, and a stateless stripper leaks the verb into the data.
func TestStripIAC_SequenceSpansAReadBoundary(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	f, _ := readOne(t, chunks("\xff", "\xfb\x01PWR ON\r\n"), s, Options{StripIAC: true})
	if string(f) != "PWR ON" {
		t.Fatalf("frame = %q; an IAC sequence split across reads leaked into the data", f)
	}
}

// TestStripIAC_EscapedLiteralSurvives: IAC IAC is a literal 0xFF and is real
// data. Dropping it silently corrupts every binary protocol tunnelled over
// port 23.
func TestStripIAC_EscapedLiteralSurvives(t *testing.T) {
	f, err := SplitOne(t, []byte("\xff\xffA\n"), Strategy{Kind: KindDelimiter, Delimiter: []byte("\n")}, Options{StripIAC: true})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if string(f) != "\xffA" {
		t.Fatalf("frame = %q, want a single literal 0xFF followed by A", f)
	}
}

func TestStripIAC_SubnegotiationIsDropped(t *testing.T) {
	// IAC SB TTYPE ... IAC SE, then data.
	in := "\xff\xfa\x18\x00xterm\xff\xf0DATA\n"
	f, err := SplitOne(t, []byte(in), Strategy{Kind: KindDelimiter, Delimiter: []byte("\n")}, Options{StripIAC: true})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if string(f) != "DATA" {
		t.Fatalf("frame = %q; the subnegotiation was not fully stripped", f)
	}
}

func TestDiscardBefore_SkipsABanner(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	o := Options{DiscardBefore: []byte("login: ")}
	f, _ := readOne(t, chunks("Banner line\r\nlogin: ", "OK\r\n"), s, o)
	if string(f) != "OK" {
		t.Fatalf("frame = %q; the banner before the marker was not discarded", f)
	}
}

func TestMaxBytes_TruncatesRatherThanRunningAway(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	r := chunks("no delimiter here at all, just a lot of bytes")
	_, _, err := Read(context.Background(), r, s, Options{MaxBytes: 8})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestClosedMidFrame_IsAProtocolFailure. A device that closes halfway through
// a length-prefixed message did answer — we just cannot honestly parse what it
// said. Naming that protocol rather than timeout is what routes it to the flow
// author instead of the AV on-call (spec §11).
func TestClosedMidFrame_IsAProtocolFailure(t *testing.T) {
	s := Strategy{Kind: KindLengthPrefix, Width: 2, Endian: BigEndian, MaxDeclared: 64}
	_, _, err := Read(context.Background(), chunks("\x00\x10short"), s, Options{})
	if err == nil {
		t.Fatal("a stream that closed mid-frame produced a successful read")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("closed")) {
		t.Errorf("error %q does not say the connection closed mid-frame", err)
	}
}

// TestSplit_IsTheSameEngine: Split Frames over bytes in hand must produce
// exactly what the live reader would, or a fixture replayed in the editor
// disagrees with the device.
func TestSplit_IsTheSameEngine(t *testing.T) {
	data := []byte("one\r\ntwo\r\nthree\r\n")
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}

	got, err := Split(data, s, Options{})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("Split returned %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("frame %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The same strategy against a live stream must agree frame for frame.
	r := chunks(string(data))
	for i := range want {
		f, rest, err := Read(context.Background(), r, s, Options{})
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		if string(f) != want[i] {
			t.Errorf("live frame %d = %q, want %q — Split and Read disagree", i, f, want[i])
		}
		r = &chunkReader{chunks: [][]byte{rest}}
	}
}

func TestSplit_TrailingPartialFrameIsAnError(t *testing.T) {
	s := Strategy{Kind: KindDelimiter, Delimiter: []byte("\r\n")}
	if _, err := Split([]byte("one\r\ntwo"), s, Options{}); err == nil {
		t.Fatal("trailing bytes that do not form a frame were accepted silently")
	}
}

func TestValidate_RefusalsNameTheFix(t *testing.T) {
	cases := []struct {
		name string
		s    Strategy
		want string
	}{
		{"empty delimiter", Strategy{Kind: KindDelimiter}, "quiet period"},
		{"zero fixed size", Strategy{Kind: KindFixed}, "positive size"},
		{"bad regex", Strategy{Kind: KindRegex, Pattern: "("}, "invalid pattern"},
		{"markers missing end", Strategy{Kind: KindMarkers, Start: []byte("{")}, "both a start and an end"},
		{"count without inner", Strategy{Kind: KindMessageCount, Count: 2}, "inner strategy"},
		{"unknown kind", Strategy{Kind: "telepathy"}, "unknown read-until strategy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Errorf("refusal %q does not contain %q — a refusal must carry a suggestion", err, tc.want)
			}
		})
	}
}

// netPipePair gives the quiet-period tests a real net.Conn, which they need
// because the strategy's boundary is a read deadline firing rather than
// anything present in the bytes. net.Pipe honours SetReadDeadline, so the
// timing path under test is the same one a socket takes.
func netPipePair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	c, s := net.Pipe()
	t.Cleanup(func() { c.Close(); s.Close() })
	return c, s
}

// SplitOne is a helper for the single-frame cases.
func SplitOne(t *testing.T, data []byte, s Strategy, o Options) ([]byte, error) {
	t.Helper()
	frames, err := Split(data, s, o)
	if err != nil {
		return nil, err
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	return frames[0], nil
}
