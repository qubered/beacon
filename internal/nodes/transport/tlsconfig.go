package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

// TLSConfig is the TLS half of any transport that can wrap its connection.
type TLSConfig struct {
	// ServerName overrides SNI and the name checked against the certificate.
	// It is needed whenever a device is reached by address rather than name,
	// which on a venue LAN is most of the time.
	ServerName string `json:"server_name,omitempty"`

	// InsecureSkipVerify disables chain and name verification. Real AV gear
	// ships self-signed certificates that never get replaced, so refusing to
	// offer this would just push people to a worse workaround — but see
	// Fingerprint, which is the answer that does not give up authentication.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`

	// CAPEM is a PEM bundle to verify against instead of the system roots.
	CAPEM string `json:"ca_pem,omitempty"`

	// Fingerprint pins the leaf certificate by SHA-256, written as hex with
	// or without colons.
	//
	// This is the right answer for self-signed device certificates: it
	// authenticates the specific device without trusting anything else, where
	// InsecureSkipVerify authenticates nothing at all.
	Fingerprint string `json:"fingerprint,omitempty"`

	MinVersion string `json:"min_version,omitempty"`
}

func (c TLSConfig) clientConfig(host string) (*tls.Config, error) {
	name := c.ServerName
	if name == "" {
		name = host
	}
	tc := &tls.Config{
		ServerName:         name,
		InsecureSkipVerify: c.InsecureSkipVerify || c.Fingerprint != "",
	}

	// A pinned fingerprint replaces chain verification rather than adding to
	// it: a self-signed certificate cannot chain to anything, and a pin that
	// only applied on top of a passing chain would never fire for the devices
	// that need it. The pin is checked in checkPin after the handshake.

	if c.CAPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(c.CAPEM)) {
			return nil, fmt.Errorf("ca_pem contains no usable certificates")
		}
		tc.RootCAs = pool
	}

	switch strings.ToLower(c.MinVersion) {
	case "", "1.2":
		tc.MinVersion = tls.VersionTLS12
	case "1.0":
		tc.MinVersion = tls.VersionTLS10
	case "1.1":
		tc.MinVersion = tls.VersionTLS11
	case "1.3":
		tc.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("unsupported TLS min_version %q — use 1.0, 1.1, 1.2 or 1.3", c.MinVersion)
	}
	return tc, nil
}

// checkPin verifies the leaf certificate's SHA-256 fingerprint.
func (c TLSConfig) checkPin(state tls.ConnectionState) error {
	if c.Fingerprint == "" {
		return nil
	}
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("fingerprint pinning is configured but the peer presented no certificate")
	}
	want := normalizeFingerprint(c.Fingerprint)
	got := fingerprintOf(state.PeerCertificates[0])
	if want != got {
		return fmt.Errorf("certificate fingerprint %s does not match the pinned %s", got, want)
	}
	return nil
}

func fingerprintOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func normalizeFingerprint(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "", "-", "").Replace(s))
}
