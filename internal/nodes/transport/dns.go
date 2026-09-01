package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// DNSQueryConfig configures transport.dns_query.
type DNSQueryConfig struct {
	Name string `json:"name"`

	// Type is A, AAAA, PTR, SRV, TXT or CNAME.
	Type string `json:"type"`

	// Resolver overrides the system resolver, as host or host:port. Checking
	// a specific nameserver is most of why this node exists: "is the venue's
	// DNS answering" is a different question from "can this agent resolve".
	Resolver string `json:"resolver,omitempty"`
}

type dnsQueryNode struct{ cfg DNSQueryConfig }

func newDNSQuery(n graph.Node) (runtime.Executable, error) {
	var cfg DNSQueryConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.dns_query: invalid config: %w", err)
		}
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("transport.dns_query needs a name to look up")
	}
	cfg.Type = strings.ToUpper(cfg.Type)
	switch cfg.Type {
	case "A", "AAAA", "PTR", "SRV", "TXT", "CNAME":
	case "":
		cfg.Type = "A"
	default:
		return nil, fmt.Errorf("transport.dns_query: unsupported record type %q — use A, AAAA, PTR, SRV, TXT or CNAME", cfg.Type)
	}
	return &dnsQueryNode{cfg: cfg}, nil
}

// Execute performs the lookup and emits the answers plus how long it took.
//
// The output is a list of records rather than bytes: unlike TCP or HTTP there
// is no device payload here to hand to a Decode, and re-encoding parsed
// answers back into wire format so a downstream node could parse them again
// would be ceremony, not honesty (invariant I1 exists so a device's bytes are
// not silently reinterpreted, and there are none).
func (d *dnsQueryNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	resolver, err := d.resolver(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	started := time.Now()
	answers, err := d.lookup(ctx, resolver)
	if err != nil {
		return nil, fail(err, "resolving %s %s: %v", d.cfg.Type, d.cfg.Name, err)
	}
	elapsed := time.Since(started)

	list := make([]any, len(answers))
	for i, a := range answers {
		list[i] = a
	}

	return runtime.Outputs{
		"answers": {Type: types.List(types.Record()), Value: list},
		"timing":  {Type: types.Record(), Value: frame.Record{"query_ms": float64(elapsed.Nanoseconds()) / 1e6}},
	}, nil
}

// resolver builds a net.Resolver whose queries go through the egress policy.
//
// A resolver override dials a nameserver, which is an outbound connection like
// any other and is checked like one. Without this an operator could reach an
// arbitrary host on port 53 from a node that looks like it only does lookups.
func (d *dnsQueryNode) resolver(ctx context.Context) (*net.Resolver, error) {
	if d.cfg.Resolver == "" {
		return &net.Resolver{PreferGo: true}, nil
	}

	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, err
	}

	host, port := d.cfg.Resolver, 53
	if h, p, err := net.SplitHostPort(d.cfg.Resolver); err == nil {
		host = h
		if n, err := parsePort(p); err == nil {
			port = n
		}
	}

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, network, host, port)
		},
	}, nil
}

func (d *dnsQueryNode) lookup(ctx context.Context, r *net.Resolver) ([]frame.Record, error) {
	switch d.cfg.Type {
	case "A", "AAAA":
		network := "ip4"
		if d.cfg.Type == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, d.cfg.Name)
		if err != nil {
			return nil, err
		}
		out := make([]frame.Record, len(ips))
		for i, ip := range ips {
			out[i] = frame.Record{"address": ip.String()}
		}
		return out, nil

	case "PTR":
		names, err := r.LookupAddr(ctx, d.cfg.Name)
		if err != nil {
			return nil, err
		}
		return stringRecords(names, "name"), nil

	case "CNAME":
		cname, err := r.LookupCNAME(ctx, d.cfg.Name)
		if err != nil {
			return nil, err
		}
		return []frame.Record{{"name": cname}}, nil

	case "TXT":
		txts, err := r.LookupTXT(ctx, d.cfg.Name)
		if err != nil {
			return nil, err
		}
		return stringRecords(txts, "text"), nil

	case "SRV":
		// An empty service and proto means cfg.Name is already a full
		// _service._proto.name, which is how anyone reading a protocol
		// document will have it written down.
		_, srvs, err := r.LookupSRV(ctx, "", "", d.cfg.Name)
		if err != nil {
			return nil, err
		}
		sort.Slice(srvs, func(i, j int) bool {
			if srvs[i].Priority != srvs[j].Priority {
				return srvs[i].Priority < srvs[j].Priority
			}
			return srvs[i].Target < srvs[j].Target
		})
		out := make([]frame.Record, len(srvs))
		for i, s := range srvs {
			out[i] = frame.Record{
				"target":   s.Target,
				"port":     int64(s.Port),
				"priority": int64(s.Priority),
				"weight":   int64(s.Weight),
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported record type %q", d.cfg.Type)
}

func stringRecords(vals []string, key string) []frame.Record {
	out := make([]frame.Record, len(vals))
	for i, v := range vals {
		out[i] = frame.Record{key: v}
	}
	return out
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "transport.dns_query",
		Title:               "DNS Query",
		Summary:             "Resolve a name and return the answers, optionally against a specific nameserver.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"dns", "resolve", "lookup", "nameserver", "a record", "ptr", "srv", "txt"},
		ConfigSchemaVersion: 1,
		Outputs: []registry.Port{
			{Name: "answers", Type: types.List(types.Record())},
			{Name: "timing", Type: types.Record()},
		},
		PerformsEgress: true,
		New:            newDNSQuery,
	})
}
