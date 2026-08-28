package runtime

import (
	"context"
	"time"

	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
)

// errorPort is the implicit error output every node has (spec §6.2). It is not
// declared by PortMeta.Outputs — the engine recognises it by name uniformly, so
// a node type needs no special registration to gain one.
const errorPort graph.PortName = "error"

type edgeState uint8

const (
	edgePending edgeState = iota
	edgeDelivered
	edgeSkipped
)

type edgeRT struct {
	edge  graph.Edge
	state edgeState
	frame frame.Frame
}

type nodeStatus uint8

const (
	stPending nodeStatus = iota
	stReady
	stRunning
	stDone
	stError
	stSkipped
)

// nodeExecResult is what a dispatched goroutine reports back to the
// coordinator.
type nodeExecResult struct {
	node    graph.NodeID
	out     Outputs
	err     error
	started time.Time
	ended   time.Time
}

// run holds all scheduler state for one execution of Run. Every field here is
// touched only by the coordinator goroutine (the one running the loop in
// Run), which is what lets the whole scheduler go without a mutex: node
// executions happen in separate goroutines, but they only ever communicate
// back over the results channel.
type run struct {
	ctx    context.Context
	cancel context.CancelFunc

	g       *graph.Graph
	factory Factory
	meta    PortMeta
	bounds  config.Bounds
	rc      *RunContext
	cap     Capturer

	nodesByID map[graph.NodeID]graph.Node
	inByPort  map[graph.NodeID]map[graph.PortName][]*edgeRT
	outByPort map[graph.NodeID]map[graph.PortName][]*edgeRT

	status       map[graph.NodeID]nodeStatus
	portResolved map[graph.NodeID]map[graph.PortName]bool
	pendingPorts map[graph.NodeID]int

	notFinished   int
	nodeExecCount int
	sem           chan struct{}
	results       chan nodeExecResult

	aborted bool
	res     *Result
}

// Run executes g to completion, to a run-ending failure, or to the deadline in
// bounds, whichever comes first.
func Run(ctx context.Context, g *graph.Graph, factory Factory, meta PortMeta, bounds config.Bounds, rc *RunContext, cap Capturer) *Result {
	bounds, _ = bounds.Clamp()
	runCtx, cancel := context.WithTimeout(ctx, bounds.RunWallClock)
	defer cancel()

	r := &run{
		ctx: runCtx, cancel: cancel,
		g: g, factory: factory, meta: meta, bounds: bounds, rc: rc, cap: cap,
		nodesByID:    make(map[graph.NodeID]graph.Node, len(g.Nodes)),
		inByPort:     make(map[graph.NodeID]map[graph.PortName][]*edgeRT),
		outByPort:    make(map[graph.NodeID]map[graph.PortName][]*edgeRT),
		status:       make(map[graph.NodeID]nodeStatus, len(g.Nodes)),
		portResolved: make(map[graph.NodeID]map[graph.PortName]bool, len(g.Nodes)),
		pendingPorts: make(map[graph.NodeID]int, len(g.Nodes)),
		sem:          make(chan struct{}, max1(bounds.ConcurrentBranches)),
		results:      make(chan nodeExecResult, max1(len(g.Nodes))),
		notFinished:  len(g.Nodes),
		res:          &Result{Nodes: make(map[graph.NodeID]NodeRun, len(g.Nodes))},
	}

	for _, n := range g.Nodes {
		r.nodesByID[n.ID] = n
		r.status[n.ID] = stPending
		r.portResolved[n.ID] = map[graph.PortName]bool{}
	}
	for _, e := range g.Edges {
		er := &edgeRT{edge: e}
		if r.inByPort[e.To.Node] == nil {
			r.inByPort[e.To.Node] = map[graph.PortName][]*edgeRT{}
		}
		r.inByPort[e.To.Node][e.To.Port] = append(r.inByPort[e.To.Node][e.To.Port], er)
		if r.outByPort[e.From.Node] == nil {
			r.outByPort[e.From.Node] = map[graph.PortName][]*edgeRT{}
		}
		r.outByPort[e.From.Node][e.From.Port] = append(r.outByPort[e.From.Node][e.From.Port], er)
	}
	for _, n := range g.Nodes {
		r.pendingPorts[n.ID] = len(r.inByPort[n.ID])
	}

	// Seed: any node with zero incoming edges has nothing to wait for.
	for _, n := range g.Nodes {
		if r.pendingPorts[n.ID] == 0 {
			r.maybeReady(n.ID)
		}
	}

	for r.notFinished > 0 && !r.aborted {
		select {
		case <-r.ctx.Done():
			r.abort(deadlineOrCancelErr(r.ctx))
		case res := <-r.results:
			r.handleResult(res)
		}
	}

	if r.aborted {
		r.drainInFlight()
	}

	r.finalizeStatus()
	return r.res
}

// drainInFlight gives goroutines that were already running at abort time a
// short window to report in, so their capture entries are not lost. It never
// blocks indefinitely — a node that ignores ctx cancellation entirely is a
// node bug that M2's per-transport abort adapters exist to prevent, not
// something the scheduler can wait out forever.
func (r *run) drainInFlight() {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case res := <-r.results:
			r.recordOnly(res)
		case <-deadline:
			return
		default:
			return
		}
	}
}

func (r *run) abort(err error) {
	if r.aborted {
		return
	}
	r.aborted = true
	r.res.Err = err
	r.cancel()
}

// maybeReady promotes a node to ready and dispatches it once every input port
// has resolved.
func (r *run) maybeReady(id graph.NodeID) {
	if r.status[id] != stPending {
		return
	}
	if r.pendingPorts[id] > 0 {
		return
	}
	r.status[id] = stReady
	r.dispatch(id)
}

func (r *run) dispatch(id graph.NodeID) {
	r.nodeExecCount++
	if r.nodeExecCount > r.bounds.NodeExecutions {
		r.abort(ErrNodeBudgetExceeded)
		return
	}
	r.status[id] = stRunning
	n := r.nodesByID[id]

	exec, ferr := r.factory(n)
	if ferr != nil {
		r.results <- nodeExecResult{node: id, err: ferr, started: time.Now(), ended: time.Now()}
		return
	}

	in := Inputs{}
	for port, edges := range r.inByPort[id] {
		for _, e := range edges {
			if e.state == edgeDelivered {
				in[port] = e.frame
				break
			}
		}
	}

	ctx, rc, results := r.ctx, r.rc, r.results
	go func() {
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			results <- nodeExecResult{node: id, err: ctx.Err(), started: time.Now(), ended: time.Now()}
			return
		}
		defer func() { <-r.sem }()

		started := time.Now()
		out, err := exec.Execute(ctx, rc, in)
		results <- nodeExecResult{node: id, out: out, err: err, started: started, ended: time.Now()}
	}()
}

// handleResult processes one node's completion: records it, fires or skips
// its output edges, and cascades port resolution to every downstream node
// those edges touch.
func (r *run) handleResult(res nodeExecResult) {
	id := res.node
	if r.status[id] == stDone || r.status[id] == stError || r.status[id] == stSkipped {
		return // already finalized via another path (e.g. aborted then drained)
	}
	nt := r.nodesByID[id].Type

	if res.err != nil {
		fail := toFailure(id, res.err)
		r.finish(id, stError)
		r.res.Nodes[id] = NodeRun{Node: id, State: NodeError, Failure: &fail, Started: res.started, Ended: res.ended}
		if r.cap != nil {
			r.cap.Record(id, nil, nil, &fail, false, res.started, res.ended)
		}

		errEdges := r.outByPort[id][errorPort]
		if len(errEdges) == 0 {
			// Unconnected error port: the run fails outright, with full
			// context captured (spec §6.2).
			r.abort(&UnhandledFailure{Node: id, Failure: fail})
			return
		}
		errFrame := frame.Frame{Type: types.Error(), Value: fail, ProducedAt: res.ended}
		for _, e := range errEdges {
			e.state = edgeDelivered
			e.frame = errFrame
			r.resolvePort(e.edge.To.Node, e.edge.To.Port)
		}
		// The node failed, so none of its normal outputs fired.
		for _, port := range r.meta.Outputs(nt) {
			r.skipEdgesFrom(id, port)
		}
		return
	}

	r.finish(id, stDone)
	r.res.Nodes[id] = NodeRun{Node: id, State: NodeDone, Outputs: res.out, Started: res.started, Ended: res.ended}
	if r.cap != nil {
		r.cap.Record(id, nil, res.out, nil, false, res.started, res.ended)
	}
	if name := r.nodesByID[id].Name; name != "" {
		// Prefer the conventional primary output port; fall back to the sole
		// output when a node has exactly one. A node with several named
		// outputs and no "out" port is not expected to be bound (spec §6.2
		// names a node's *output*, singular).
		if f, ok := res.out["out"]; ok {
			r.rc.Bind(name, f)
		} else if len(res.out) == 1 {
			for _, f := range res.out {
				r.rc.Bind(name, f)
			}
		}
	}

	for _, port := range r.meta.Outputs(nt) {
		if f, fired := res.out[port]; fired {
			for _, e := range r.outByPort[id][port] {
				e.state = edgeDelivered
				e.frame = f
				r.resolvePort(e.edge.To.Node, e.edge.To.Port)
			}
		} else {
			r.skipEdgesFrom(id, port)
		}
	}
	// No error occurred, so a wired error port (defensive authoring) is skipped.
	r.skipEdgesFrom(id, errorPort)
}

// recordOnly is used only during drainInFlight: it captures a late result
// without touching scheduler state, since the run has already been decided.
func (r *run) recordOnly(res nodeExecResult) {
	id := res.node
	if _, already := r.res.Nodes[id]; already {
		return
	}
	if res.err != nil {
		fail := toFailure(id, res.err)
		r.res.Nodes[id] = NodeRun{Node: id, State: NodeError, Failure: &fail, Started: res.started, Ended: res.ended}
	} else {
		r.res.Nodes[id] = NodeRun{Node: id, State: NodeDone, Outputs: res.out, Started: res.started, Ended: res.ended}
	}
}

func (r *run) skipEdgesFrom(id graph.NodeID, port graph.PortName) {
	for _, e := range r.outByPort[id][port] {
		if e.state == edgePending {
			e.state = edgeSkipped
			r.resolvePort(e.edge.To.Node, e.edge.To.Port)
		}
	}
}

// resolvePort is the branch-join rule (spec §6.1). A port is resolved — ready
// to contribute to its node's readiness — the moment ANY of its incoming
// edges delivers a frame; it does not wait for the rest, because on a genuine
// branch join the untaken branch never produces anything and waiting for it
// would deadlock forever.
//
// Only when every incoming edge has settled (delivered or skipped) and NONE
// delivered does the port resolve to "will never receive a value" — at which
// point a required port skips the whole node, cascading downstream.
func (r *run) resolvePort(id graph.NodeID, port graph.PortName) {
	if r.portResolved[id][port] {
		return
	}
	ins := r.inByPort[id][port]

	for _, e := range ins {
		if e.state == edgeDelivered {
			r.portResolved[id][port] = true
			r.pendingPorts[id]--
			r.maybeReady(id)
			return
		}
	}

	for _, e := range ins {
		if e.state == edgePending {
			return // a reachable branch hasn't settled yet — keep waiting
		}
	}

	// Every edge settled and none delivered.
	r.portResolved[id][port] = true
	r.pendingPorts[id]--
	if r.meta.Required(r.nodesByID[id].Type, port) {
		r.skip(id)
		return
	}
	r.maybeReady(id)
}

// skip marks a node as never going to run because a required input can never
// arrive, and cascades that fact to every downstream node through its outputs
// — including its error port, since a skipped node cannot fail either.
func (r *run) skip(id graph.NodeID) {
	if r.status[id] == stDone || r.status[id] == stError || r.status[id] == stSkipped {
		return
	}
	r.finish(id, stSkipped)
	r.res.Nodes[id] = NodeRun{Node: id, State: NodeSkipped}
	if r.cap != nil {
		r.cap.Record(id, nil, nil, nil, true, time.Time{}, time.Time{})
	}

	nt := r.nodesByID[id].Type
	for _, port := range append(append([]graph.PortName{}, r.meta.Outputs(nt)...), errorPort) {
		r.skipEdgesFrom(id, port)
	}
}

func (r *run) finish(id graph.NodeID, st nodeStatus) {
	if r.status[id] == stDone || r.status[id] == stError || r.status[id] == stSkipped {
		return
	}
	r.status[id] = st
	r.notFinished--
}

// finalizeStatus applies spec §6.2's rule: a run that never fires a terminal
// (Emit Status) node produces StatusUnknown plus a warning, not an error — the
// execution completed, it simply reached no verdict.
func (r *run) finalizeStatus() {
	if r.res.Err != nil {
		r.res.Status = frame.StatusUnknown
		return
	}
	var found bool
	for id, nr := range r.res.Nodes {
		if nr.State != NodeDone || !r.meta.Terminal(r.nodesByID[id].Type) {
			continue
		}
		for _, f := range nr.Outputs {
			if s, ok := f.Value.(frame.Status); ok {
				if found {
					r.res.Warning = "multiple Emit Status nodes reached; using the first observed"
					continue
				}
				r.res.Status = s
				found = true
			}
		}
	}
	if !found {
		r.res.Status = frame.StatusUnknown
		if r.res.Warning == "" {
			r.res.Warning = "run did not reach an Emit Status node"
		}
	}
}

func toFailure(node graph.NodeID, err error) frame.Failure {
	if f, ok := err.(frame.Failure); ok {
		if f.Node == "" {
			f.Node = string(node)
		}
		return f
	}
	if f, ok := err.(*frame.Failure); ok && f != nil {
		ff := *f
		if ff.Node == "" {
			ff.Node = string(node)
		}
		return ff
	}
	return frame.Failure{Class: frame.ClassInternal, Node: string(node), Message: err.Error()}
}

func deadlineOrCancelErr(ctx context.Context) error {
	if ctx.Err() == context.DeadlineExceeded {
		return ErrDeadlineExceeded
	}
	return ctx.Err()
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
