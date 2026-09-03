package executor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/agent/ratelimit"
	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/nodes/registry"

	_ "github.com/qubered/beacon/internal/nodes/byteops"
	_ "github.com/qubered/beacon/internal/nodes/control"
	_ "github.com/qubered/beacon/internal/nodes/emit"
	_ "github.com/qubered/beacon/internal/nodes/parse"
)

// recordingSink stands in for the spool.
type recordingSink struct {
	mu       sync.Mutex
	results  []Result
	captures []json.RawMessage
}

func (s *recordingSink) Add(result, capture json.RawMessage) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Result
	if err := json.Unmarshal(result, &r); err != nil {
		return 0, err
	}
	s.results = append(s.results, r)
	s.captures = append(s.captures, capture)
	return uint64(len(s.results)), nil
}

func (s *recordingSink) last() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.results[len(s.results)-1]
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.results)
}

type staticFlows struct {
	g   *graph.Graph
	err error
}

func (f staticFlows) Graph(ctx context.Context, id string) (*graph.Graph, error) {
	return f.g, f.err
}

// countingFlows counts how many times a graph was fetched, which is how the
// retry tests observe whole-flow re-execution.
type countingExec struct {
	mu    sync.Mutex
	calls int
	fail  bool
	class frame.ErrorClass
}

func (c *countingExec) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	c.mu.Lock()
	c.calls++
	fail := c.fail
	class := c.class
	c.mu.Unlock()
	if fail {
		return nil, frame.Fail(class, "deliberate failure")
	}
	return runtime.Outputs{"out": {Value: frame.StatusUp}}, nil
}

func (c *countingExec) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// statusGraph is the smallest flow that reaches a terminal Emit Status.
//
// Emit Status takes a status on its "in" port, and no M1 node manufactures one
// from nothing, so the root's input is injected by wrapping the factory — the
// same thing beaconctl's --fixture does and for the same reason.
func statusGraph(t *testing.T) *graph.Graph {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"reason": "ok"})
	return &graph.Graph{
		Nodes: []graph.Node{{ID: "status", Type: "emit.emit_status", Config: cfg}},
	}
}

// feedStatusRoot supplies the "status" node its input frame, standing in for
// the upstream Assert a real flow would have.
func feedStatusRoot(inner runtime.Factory) runtime.Factory {
	return func(n graph.Node) (runtime.Executable, error) {
		exec, err := inner(n)
		if err != nil || n.ID != "status" {
			return exec, err
		}
		return injectStatus{inner: exec}, nil
	}
}

type injectStatus struct{ inner runtime.Executable }

func (w injectStatus) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	if in == nil {
		in = runtime.Inputs{}
	}
	in["in"] = frame.Frame{Value: frame.StatusUp}
	return w.inner.Execute(ctx, rc, in)
}

func newExecutor(t *testing.T, flows Flows, sink Sink) *Executor {
	t.Helper()
	reg := registry.Default
	return &Executor{
		Flows:    flows,
		Registry: reg.PortMeta(),
		Factory:  feedStatusRoot(reg.Factory()),
		Limiter:  ratelimit.New(),
		Sink:     sink,
		Bounds:   config.DefaultBounds(),
	}
}

func sampleMonitor() Monitor {
	return Monitor{
		ID: "m1", Name: "power", DeviceID: "d1", FlowVersionID: "fv1",
		Interval: time.Minute, Timeout: 5 * time.Second,
	}
}

func sampleDevice() Device {
	return Device{ID: "d1", Name: "projector-1", Host: "10.0.0.5"}
}

// --- var precedence ---

// TestMergeVars_MonitorOverridesDeviceOverridesFlow is spec §6.2's precedence,
// and the rule that lets one flow serve fourteen devices. Reversing any pair
// makes the more specific value lose to the more general one, and the symptom
// is a monitor silently using the wrong channel count — a wrong answer rather
// than an error.
func TestMergeVars_MonitorOverridesDeviceOverridesFlow(t *testing.T) {
	g := &graph.Graph{Defaults: map[string]any{
		"channels": 4, "timeout": "flow", "only_flow": true,
	}}
	d := Device{ID: "d1", Name: "dev", Host: "h", Vars: map[string]any{
		"channels": 8, "timeout": "device", "only_device": true,
	}}
	m := Monitor{ID: "m1", Vars: map[string]any{
		"channels": 16, "only_monitor": true,
	}}

	got := MergeVars(g, d, m)

	if got["channels"] != 16 {
		t.Errorf("channels = %v, want the monitor's 16 — monitor vars must win", got["channels"])
	}
	if got["timeout"] != "device" {
		t.Errorf("timeout = %v, want the device's value — device vars beat flow defaults", got["timeout"])
	}
	if got["only_flow"] != true || got["only_device"] != true || got["only_monitor"] != true {
		t.Error("a key present at only one level was lost in the merge")
	}
}

// TestMergeVars_DoesNotMutateItsInputs: merging into the device's own map would
// corrupt configuration other runs share, and the damage would surface on an
// unrelated monitor.
func TestMergeVars_DoesNotMutateItsInputs(t *testing.T) {
	g := &graph.Graph{Defaults: map[string]any{"a": 1}}
	d := Device{Vars: map[string]any{"b": 2}}
	m := Monitor{Vars: map[string]any{"c": 3}}

	MergeVars(g, d, m)

	if len(g.Defaults) != 1 || len(d.Vars) != 1 || len(m.Vars) != 1 {
		t.Fatalf("an input map was mutated: flow=%v device=%v monitor=%v", g.Defaults, d.Vars, m.Vars)
	}
}

// TestMergeVars_DeviceIdentityCannotBeSpoofed: a var named device.host must not
// redirect a transport somewhere the operator never configured.
func TestMergeVars_DeviceIdentityCannotBeSpoofed(t *testing.T) {
	g := &graph.Graph{Defaults: map[string]any{"device.host": "evil.example"}}
	d := Device{ID: "d1", Name: "real", Host: "10.0.0.5", Vars: map[string]any{"device.host": "also-evil"}}
	m := Monitor{ID: "m1", Vars: map[string]any{"device.host": "still-evil"}}

	got := MergeVars(g, d, m)
	if got["device.host"] != "10.0.0.5" {
		t.Fatalf("device.host = %v, want the real host — identity is context, not a var", got["device.host"])
	}
}

func TestMergeVars_HandlesNilGraphAndEmptyMaps(t *testing.T) {
	got := MergeVars(nil, Device{ID: "d"}, Monitor{ID: "m"})
	if got["device.id"] != "d" || got["monitor.id"] != "m" {
		t.Fatalf("identity missing from %v", got)
	}
}

// --- execution ---

func TestRun_SuccessfulRunIsSpooled(t *testing.T) {
	sink := &recordingSink{}
	e := newExecutor(t, staticFlows{g: statusGraph(t)}, sink)

	slot := time.Now().UTC().Truncate(time.Second)
	res, err := e.Run(context.Background(), sampleMonitor(), sampleDevice(), slot)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outcome != OutcomeOK {
		t.Fatalf("outcome = %s (%s), want ok", res.Outcome, res.Message)
	}
	if !res.ScheduledAt.Equal(slot) {
		t.Errorf("scheduled slot = %s, want %s — it is the execution fence", res.ScheduledAt, slot)
	}
	if sink.count() != 1 {
		t.Fatalf("%d results spooled, want 1", sink.count())
	}
	if sink.last().MonitorID != "m1" {
		t.Errorf("spooled result is for %q", sink.last().MonitorID)
	}
}

// TestRun_AlwaysProducesAResult: a run that vanished because something went
// wrong leaves a hole in history indistinguishable from a monitor nobody
// scheduled.
func TestRun_AlwaysProducesAResult(t *testing.T) {
	sink := &recordingSink{}
	e := newExecutor(t, staticFlows{err: context.DeadlineExceeded}, sink)

	res, err := e.Run(context.Background(), sampleMonitor(), sampleDevice(), time.Now())
	if err != nil {
		t.Fatalf("Run returned an error instead of a result: %v", err)
	}
	if res.Status != frame.StatusUnknown {
		t.Errorf("status = %s, want unknown when the flow could not be loaded", res.Status)
	}
	if sink.count() != 1 {
		t.Fatalf("%d results spooled; a failure must still be recorded", sink.count())
	}
}

// TestRun_ThrottledIsNotAFailure: the device never misbehaved, the schedule
// did. Counting it as a failure would walk a healthy monitor towards down.
func TestRun_ThrottledIsNotAFailure(t *testing.T) {
	sink := &recordingSink{}
	e := newExecutor(t, staticFlows{g: statusGraph(t)}, sink)
	e.Limiter.Configure("d1", ratelimit.Limits{MaxConcurrent: 1, Burst: 1})

	// Hold the only slot so the run cannot acquire one.
	held, err := e.Limiter.Acquire(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer held()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	res, err := e.Run(ctx, sampleMonitor(), sampleDevice(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeThrottled {
		t.Fatalf("outcome = %s, want throttled", res.Outcome)
	}
	if res.ErrorClass != frame.ClassNone {
		t.Errorf("error class = %s; a throttle is not an error", res.ErrorClass)
	}
	if sink.captures[0] != nil {
		t.Error("a throttled run carried a capture; nothing executed, so there is nothing to inspect")
	}
}

// TestRun_RetriesReRunTheWholeFlow: retries are a monitor property, not a node
// property (spec §6.2).
func TestRun_RetriesReRunTheWholeFlow(t *testing.T) {
	exec := &countingExec{fail: true, class: frame.ClassTimeout}
	e := newExecutor(t, staticFlows{g: &graph.Graph{
		Nodes: []graph.Node{{ID: "n", Type: "test.counting"}},
	}}, &recordingSink{})
	e.Factory = func(n graph.Node) (runtime.Executable, error) { return exec, nil }
	e.Registry = fakeMeta{}

	m := sampleMonitor()
	m.Retries = 2

	res, err := e.Run(context.Background(), m, sampleDevice(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.count(); got != 3 {
		t.Fatalf("the flow ran %d times, want 3 (the initial attempt plus 2 retries)", got)
	}
	if res.Attempt != 2 {
		t.Errorf("attempt = %d, want 2 on the final try", res.Attempt)
	}
	if res.Outcome != OutcomeFailed {
		t.Errorf("outcome = %s, want failed after exhausting retries", res.Outcome)
	}
}

// TestRun_StopsRetryingOnSuccess: a transient fault that clears must not burn
// the remaining attempts.
func TestRun_StopsRetryingOnSuccess(t *testing.T) {
	exec := &countingExec{fail: false}
	e := newExecutor(t, staticFlows{g: &graph.Graph{
		Nodes: []graph.Node{{ID: "n", Type: "test.counting"}},
	}}, &recordingSink{})
	e.Factory = func(n graph.Node) (runtime.Executable, error) { return exec, nil }
	e.Registry = fakeMeta{}

	m := sampleMonitor()
	m.Retries = 3

	if _, err := e.Run(context.Background(), m, sampleDevice(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := exec.count(); got != 1 {
		t.Fatalf("the flow ran %d times after succeeding first try, want 1", got)
	}
}

// TestRun_DoesNotRetryAnAssertionFailure: the device answered and the value was
// out of range. Re-asking produces the same answer, so retrying only delays the
// alert and spends the run's budget.
func TestRun_DoesNotRetryAnAssertionFailure(t *testing.T) {
	exec := &countingExec{fail: true, class: frame.ClassAssertion}
	e := newExecutor(t, staticFlows{g: &graph.Graph{
		Nodes: []graph.Node{{ID: "n", Type: "test.counting"}},
	}}, &recordingSink{})
	e.Factory = func(n graph.Node) (runtime.Executable, error) { return exec, nil }
	e.Registry = fakeMeta{}

	m := sampleMonitor()
	m.Retries = 3

	res, err := e.Run(context.Background(), m, sampleDevice(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.count(); got != 1 {
		t.Fatalf("an assertion failure was retried %d times; re-asking cannot change the answer", got)
	}
	if res.ErrorClass != frame.ClassAssertion {
		t.Errorf("error class = %s, want assertion — it is what routes the alert", res.ErrorClass)
	}
}

// TestRun_PreservesTheErrorClass: `protocol` going to the flow author rather
// than the AV on-call is the whole point of the classes (spec §11).
func TestRun_PreservesTheErrorClass(t *testing.T) {
	for _, class := range []frame.ErrorClass{frame.ClassTimeout, frame.ClassProtocol, frame.ClassConnectRefused} {
		exec := &countingExec{fail: true, class: class}
		e := newExecutor(t, staticFlows{g: &graph.Graph{
			Nodes: []graph.Node{{ID: "n", Type: "test.counting"}},
		}}, &recordingSink{})
		e.Factory = func(n graph.Node) (runtime.Executable, error) { return exec, nil }
		e.Registry = fakeMeta{}

		res, err := e.Run(context.Background(), sampleMonitor(), sampleDevice(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if res.ErrorClass != class {
			t.Errorf("error class = %s, want %s", res.ErrorClass, class)
		}
	}
}

// TestWorstCaseDuration is the bound publishing checks against the interval in
// M6. Overlapping runs are the schedule falling apart, not a slow monitor.
func TestWorstCaseDuration(t *testing.T) {
	m := Monitor{Timeout: 10 * time.Second, Retries: 2, RetryInterval: 5 * time.Second}
	// (2+1)*10s + 2*5s = 40s
	if got, want := m.WorstCaseDuration(), 40*time.Second; got != want {
		t.Fatalf("worst case = %s, want %s", got, want)
	}
}

// fakeMeta is a PortMeta for the synthetic single-node graphs above.
type fakeMeta struct{}

func (fakeMeta) Outputs(string) []graph.PortName      { return []graph.PortName{"out"} }
func (fakeMeta) Required(string, graph.PortName) bool { return false }
func (fakeMeta) Terminal(string) bool                 { return true }
