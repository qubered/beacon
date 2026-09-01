package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/capture"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/nodes/registry"

	// The catalogue beaconctl can execute against. An agent's own main
	// package assembles its catalogue the same way, via these init() side
	// effects — this is the reference point for "what nodes exist."
	_ "github.com/qubered/beacon/internal/nodes/byteops"
	_ "github.com/qubered/beacon/internal/nodes/control"
	_ "github.com/qubered/beacon/internal/nodes/emit"
	_ "github.com/qubered/beacon/internal/nodes/parse"
	_ "github.com/qubered/beacon/internal/nodes/transport"
)

// runFlowCommand implements `beaconctl flow run`.
//
// Two ways bytes enter a graph. A --fixture feeds recorded bytes to a named
// root node, which is how a Pack's flows are regression-tested with the gear
// locked in a venue (decision D27). Or a transport node opens a real socket,
// in which case --allow rules are required: the run carries an egress policy
// exactly as an agent's would, and a flow that tries to reach outside it is
// refused here for the same reason it would be refused in a rack.
func runFlowCommand(args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("usage: beaconctl flow run --graph <file> [--fixture <file> --root <node-id>]")
	}
	fs := flag.NewFlagSet("flow run", flag.ContinueOnError)
	graphPath := fs.String("graph", "", "path to a flow graph JSON file (required)")
	fixturePath := fs.String("fixture", "", "path to raw bytes fed to --root's \"in\" port")
	rootID := fs.String("root", "", "node ID that receives --fixture (required if --fixture is set)")
	timeout := fs.Duration("timeout", 30*time.Second, "run wall-clock deadline")
	var allow allowFlag
	fs.Var(&allow, "allow", "egress allow rule, repeatable: CIDR,proto[,ports] (e.g. 10.0.0.0/8,tcp,1-65535)")
	allowLoopback := fs.Bool("allow-loopback", false, "permit loopback addresses — for a device simulator on this host, never in production")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *graphPath == "" {
		return fmt.Errorf("--graph is required")
	}

	gBytes, err := os.ReadFile(*graphPath)
	if err != nil {
		return fmt.Errorf("reading graph: %w", err)
	}
	var g graph.Graph
	if err := json.Unmarshal(gBytes, &g); err != nil {
		return fmt.Errorf("parsing graph: %w", err)
	}

	reg := registry.Default
	if err := g.Validate(reg.PortTypes()); err != nil {
		return fmt.Errorf("graph is invalid: %w", err)
	}

	factory := reg.Factory()
	if *fixturePath != "" {
		if *rootID == "" {
			return fmt.Errorf("--root is required when --fixture is set")
		}
		fixtureBytes, err := os.ReadFile(*fixturePath)
		if err != nil {
			return fmt.Errorf("reading fixture: %w", err)
		}
		factory = wrapFixtureRoot(factory, graph.NodeID(*rootID), fixtureBytes)
	}

	bounds := config.DefaultBounds()
	bounds.RunWallClock = *timeout

	rc := runtime.NewRunContext(fmt.Sprintf("cli-%d", time.Now().UnixNano()), time.Now())
	rec := capture.NewRecorder(bounds.CapturedFrameSize)

	// The dialer rides on the context, which is how a transport node reaches
	// the egress policy without the engine knowing transports exist and
	// without the node knowing where it is running.
	ctx := egress.WithDialer(context.Background(), &egress.Dialer{
		Policy:  egress.Policy{Name: "beaconctl", Allow: allow, AllowLoopback: *allowLoopback},
		Auditor: stderrAuditor{w: os.Stderr},
	})

	res := runtime.Run(ctx, &g, factory, reg.PortMeta(), bounds, rc, rec)
	printFlowResult(res, rec)

	if res.Err != nil {
		return res.Err
	}
	return nil
}

// wrapFixtureRoot makes factory hand rootID a literal bytes frame on its "in"
// port instead of whatever (nothing) its wiring would otherwise supply.
func wrapFixtureRoot(factory runtime.Factory, rootID graph.NodeID, fixture []byte) runtime.Factory {
	return func(n graph.Node) (runtime.Executable, error) {
		exec, err := factory(n)
		if err != nil {
			return nil, err
		}
		if n.ID != rootID {
			return exec, nil
		}
		return fixtureRootNode{inner: exec, fixture: fixture}, nil
	}
}

type fixtureRootNode struct {
	inner   runtime.Executable
	fixture []byte
}

func (w fixtureRootNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	if in == nil {
		in = runtime.Inputs{}
	}
	in["in"] = frame.Frame{Value: w.fixture}
	return w.inner.Execute(ctx, rc, in)
}

func printFlowResult(res *runtime.Result, rec *capture.Recorder) {
	entries := rec.Entries()
	byNode := make(map[graph.NodeID]capture.Entry, len(entries))
	for _, e := range entries {
		byNode[e.Node] = e
	}

	ids := make([]string, 0, len(res.Nodes))
	for id := range res.Nodes {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)

	for _, id := range ids {
		nr := res.Nodes[graph.NodeID(id)]
		switch nr.State {
		case runtime.NodeSkipped:
			fmt.Printf("  %-20s skipped\n", id)
		case runtime.NodeError:
			fmt.Printf("  %-20s error    %s\n", id, nr.Failure.Error())
		case runtime.NodeDone:
			e := byNode[graph.NodeID(id)]
			fmt.Printf("  %-20s done     %s\n", id, renderOutputs(e))
		}
	}

	fmt.Println()
	fmt.Printf("status: %s\n", res.Status)
	if res.Warning != "" {
		fmt.Printf("warning: %s\n", res.Warning)
	}
	if res.Err != nil {
		fmt.Printf("run failed: %v\n", res.Err)
	}
}

func renderOutputs(e capture.Entry) string {
	if len(e.Outputs) == 0 {
		return ""
	}
	ports := make([]string, 0, len(e.Outputs))
	for p := range e.Outputs {
		ports = append(ports, string(p))
	}
	sort.Strings(ports)
	out := ""
	for i, p := range ports {
		if i > 0 {
			out += " "
		}
		r := e.Outputs[graph.PortName(p)]
		out += fmt.Sprintf("%s=%v", p, r.Value)
	}
	return out
}
