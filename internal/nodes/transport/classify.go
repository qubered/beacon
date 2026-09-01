package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/nodes/framing"
)

// classify maps a Go error to one of the error classes in spec §6.2.
//
// This matters more than it looks. The class is what alerting routes on, and
// the valuable distinction is `protocol` — the device answered and we misread
// it — going to the flow author rather than the AV on-call. Twelve monitors
// going protocol after a firmware update is a completely different message
// from twelve going timeout, so a lazy mapping of everything to `internal`
// destroys the signal that makes the routing worth having.
func classify(err error) frame.ErrorClass {
	switch {
	case err == nil:
		return frame.ClassNone

	case egress.IsDenied(err):
		// A denied connection is the policy working, not a device fault. It is
		// internal so it never reaches the on-call as a gear problem; the
		// security event log is where it belongs.
		return frame.ClassInternal

	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return frame.ClassTimeout

	case errors.Is(err, syscall.ECONNREFUSED):
		return frame.ClassConnectRefused

	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		// The device accepted a connection and then tore it down. That is the
		// device answering badly rather than being absent.
		return frame.ClassProtocol

	case errors.Is(err, framing.ErrTruncated):
		// The bytes arrived; the framing did not match them.
		return frame.ClassProtocol

	case isDNSError(err):
		return frame.ClassDNS

	case isTLSError(err):
		return frame.ClassTLS

	case errors.Is(err, io.ErrUnexpectedEOF):
		return frame.ClassProtocol

	default:
		return frame.ClassInternal
	}
}

// fail builds a Failure with the right class for err, so no transport has to
// remember to classify at each of its own return points.
func fail(err error, format string, args ...any) frame.Failure {
	f := frame.Fail(classify(err), format, args...)
	return f
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func isDNSError(err error) bool {
	var d *net.DNSError
	return errors.As(err, &d)
}

func isTLSError(err error) bool {
	var ce *x509.CertificateInvalidError
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ra *tls.RecordHeaderError
	var alert tls.AlertError
	return errors.As(err, &ce) || errors.As(err, &ua) || errors.As(err, &hn) ||
		errors.As(err, &ra) || errors.As(err, &alert)
}
