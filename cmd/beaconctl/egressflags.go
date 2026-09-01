package main

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/qubered/beacon/internal/agent/egress"
)

// allowFlag collects repeated --allow rules.
//
// The CLI has no configuration file yet, but it must not paper over that with
// an implicit allow-everything: a tool that quietly grants unrestricted egress
// teaches the wrong model of what the agent does, and someone will eventually
// copy its behaviour into the agent.
type allowFlag []egress.Rule

func (a *allowFlag) String() string { return fmt.Sprintf("%d rules", len(*a)) }

// Set parses "CIDR,proto[,ports]" — for example "10.0.0.0/8,tcp,1-65535" or
// "192.168.1.0/24,icmp".
func (a *allowFlag) Set(v string) error {
	parts := strings.Split(v, ",")
	if len(parts) < 2 {
		return fmt.Errorf("want CIDR,proto[,ports] — for example 10.0.0.0/8,tcp,1-65535")
	}

	prefix, err := netip.ParsePrefix(strings.TrimSpace(parts[0]))
	if err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", parts[0], err)
	}

	var proto egress.Protocol
	switch strings.ToLower(strings.TrimSpace(parts[1])) {
	case "tcp":
		proto = egress.ProtoTCP
	case "udp":
		proto = egress.ProtoUDP
	case "icmp":
		proto = egress.ProtoICMP
	default:
		return fmt.Errorf("protocol must be tcp, udp or icmp (got %q)", parts[1])
	}

	rule := egress.Rule{Prefix: prefix, Protocol: proto}
	if proto == egress.ProtoICMP {
		*a = append(*a, rule)
		return nil
	}

	if len(parts) < 3 {
		return fmt.Errorf("%s rules need a port range — for example %s,%s,1-65535", proto, parts[0], proto)
	}
	lo, hi, err := parsePortRange(strings.TrimSpace(parts[2]))
	if err != nil {
		return err
	}
	rule.MinPort, rule.MaxPort = lo, hi
	*a = append(*a, rule)
	return nil
}

func parsePortRange(s string) (int, int, error) {
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		hi = lo
	}
	loN, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port %q", lo)
	}
	hiN, err := strconv.Atoi(hi)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port %q", hi)
	}
	if loN < 1 || hiN > 65535 || loN > hiN {
		return 0, 0, fmt.Errorf("port range %q must fall within 1-65535 and be ordered", s)
	}
	return loN, hiN, nil
}

// stderrAuditor prints every egress denial, which is the CLI's stand-in for
// the security event log. Seeing the refusal and its reason is most of what
// makes the policy debuggable while authoring a flow.
type stderrAuditor struct {
	w interface{ Write([]byte) (int, error) }
}

func (a stderrAuditor) Denied(ev egress.DeniedError) {
	fmt.Fprintf(a.w, "egress denied: %s\n", ev.Reason)
}
