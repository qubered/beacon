package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// TLSInspectConfig configures transport.tls_inspect.
type TLSInspectConfig struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`

	// ServerName overrides SNI. On a venue LAN a device is usually reached by
	// address, and some appliances serve a different certificate depending on
	// the name asked for.
	ServerName string `json:"server_name,omitempty"`
}

type tlsInspectNode struct{ cfg TLSInspectConfig }

func newTLSInspect(n graph.Node) (runtime.Executable, error) {
	var cfg TLSInspectConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.tls_inspect: invalid config: %w", err)
		}
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("transport.tls_inspect needs a host")
	}
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("transport.tls_inspect port must be 1-65535 (got %d)", cfg.Port)
	}
	return &tlsInspectNode{cfg: cfg}, nil
}

// Execute handshakes and reports what the certificate says.
//
// Verification is deliberately off. This node's job is to tell you what the
// certificate *is* — including that it expired last Tuesday or is self-signed
// — and a verifying handshake would fail before it could report any of that.
// The findings are emitted as data so an Assert or Threshold node decides
// what is acceptable, which keeps the policy in the flow where an operator
// can see it rather than buried in this node.
func (t *tlsInspectNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	conn, err := dialer.Dial(ctx, "tcp", t.cfg.Host, t.cfg.Port)
	if err != nil {
		return nil, fail(err, "connecting to %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
	}
	defer conn.Close()

	name := t.cfg.ServerName
	if name == "" {
		name = t.cfg.Host
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: name, InsecureSkipVerify: true})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fail(err, "TLS handshake with %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, frame.Fail(frame.ClassProtocol, "%s:%d completed a handshake but presented no certificate", t.cfg.Host, t.cfg.Port)
	}
	leaf := state.PeerCertificates[0]

	sans := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	sans = append(sans, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		sans = append(sans, ip.String())
	}

	// Days remaining is what a threshold is actually set on, and computing it
	// here means every flow does not repeat the same date arithmetic.
	daysRemaining := time.Until(leaf.NotAfter).Hours() / 24

	return runtime.Outputs{"out": {
		Type: types.Record(),
		Value: frame.Record{
			"subject":        leaf.Subject.String(),
			"issuer":         leaf.Issuer.String(),
			"serial":         leaf.SerialNumber.String(),
			"not_before":     leaf.NotBefore.UTC().Format(time.RFC3339),
			"not_after":      leaf.NotAfter.UTC().Format(time.RFC3339),
			"days_remaining": daysRemaining,
			"expired":        time.Now().After(leaf.NotAfter),
			"self_signed":    leaf.Subject.String() == leaf.Issuer.String(),
			"sans":           sans,
			"fingerprint":    fingerprintOf(leaf),
			"tls_version":    tlsVersionName(state.Version),
			"cipher_suite":   tls.CipherSuiteName(state.CipherSuite),
		},
	}}, nil
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "1.0"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	default:
		return "unknown (0x" + strconv.FormatUint(uint64(v), 16) + ")"
	}
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return n, nil
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "transport.tls_inspect",
		Title:               "TLS Inspect",
		Summary:             "Handshake and report the certificate's expiry, issuer, subject, SANs and fingerprint.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"tls", "ssl", "certificate", "cert", "expiry", "https", "fingerprint"},
		ConfigSchemaVersion: 1,
		Outputs:             []registry.Port{{Name: "out", Type: types.Record()}},
		PerformsEgress:      true,
		New:                 newTLSInspect,
	})
}
