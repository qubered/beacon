package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/agent/ratelimit"
	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/capture"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
)

// Device is the executor's view of a device. Vars are how one flow serves
// fourteen devices with different channel counts (spec §6.2).
type Device struct {
	ID   string
	Name string
	Host string
	Tags []string
	Vars map[string]any
}

// Monitor is the executor's view of a monitor's configuration.
type Monitor struct {
	ID            string
	Name          string
	DeviceID      string
	FlowVersionID string

	Interval time.Duration
	Timeout  time.Duration

	// Retries re-run the whole flow, not a node (spec §6.2). They consume the
	// run's wall-clock budget, so worst-case duration is
	// (retries+1) x timeout + retries x retry_interval — which is what publish
	// must check against the interval in M6.
	Retries       int
	RetryInterval time.Duration

	Vars map[string]any
}

// WorstCaseDuration is the bound publishing must compare against the interval.
// Overlapping runs are the schedule falling apart, not a monitor being slow.
func (m Monitor) WorstCaseDuration() time.Duration {
	return time.Duration(m.Retries+1)*m.Timeout + time.Duration(m.Retries)*m.RetryInterval
}

// Flows resolves a flow version to the graph that produced it.
//
// An interface rather than a store dependency: the agent caches flow versions
// locally and must keep running from that cache while the link is down (D14),
// so where a graph comes from is deliberately not this package's business.
type Flows interface {
	Graph(ctx context.Context, flowVersionID string) (*graph.Graph, error)
}

// Result is one completed run, as handed to the spool.
type Result struct {
	MonitorID     string `json:"monitor_id"`
	FlowVersionID string `json:"flow_version_id"`

	// ScheduledAt is the slot, and doubles as the execution fence so a
	// replayed spool item deduplicates on insert (spec §7.3).
	ScheduledAt time.Time `json:"scheduled_at"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`

	Attempt    int              `json:"attempt"`
	Status     frame.Status     `json:"status"`
	Outcome    string           `json:"outcome"`
	ErrorClass frame.ErrorClass `json:"error_class"`
	Message    string           `json:"message,omitempty"`
}

// Outcomes, mirroring the run_outcome enum. Throttled is distinct from failed:
// the device never misbehaved, the schedule did.
const (
	OutcomeOK        = "ok"
	OutcomeFailed    = "failed"
	OutcomeThrottled = "throttled"
	OutcomeError     = "error"
)

// Sink receives completed runs. In production this is the spool; execution and
// persistence are decoupled so monitoring never stops because storage is
// unhappy (principle 8).
type Sink interface {
	Add(result, capture json.RawMessage) (uint64, error)
}

// Executor runs one monitor's flow when the scheduler says it is due.
type Executor struct {
	Flows    Flows
	Registry runtime.PortMeta
	Factory  runtime.Factory
	Limiter  *ratelimit.Limiter
	Dialer   *egress.Dialer
	Sink     Sink
	Bounds   config.Bounds
}

// Run executes a monitor's flow for one scheduled slot.
//
// It always produces a Result, including when it fails: a run that vanished
// because something went wrong leaves a hole in history that looks exactly
// like a monitor nobody scheduled.
func (e *Executor) Run(ctx context.Context, m Monitor, d Device, slot time.Time) (Result, error) {
	started := time.Now()

	// The rate limit is acquired before any work, and the device's bucket is
	// shared by every monitor bound to it (spec §8). Throttling is not a
	// failure: it records its own outcome and increments a counter, because a
	// non-zero throttle count means someone over-scheduled the device.
	release, err := e.Limiter.Acquire(ctx, m.DeviceID)
	if err != nil {
		if errors.Is(err, ratelimit.ErrThrottled) {
			return e.emit(m, slot, started, 0, Result{
				Status:  frame.StatusUnknown,
				Outcome: OutcomeThrottled,
				Message: fmt.Sprintf("device %s rate limit reached", d.Name),
			})
		}
		return e.emit(m, slot, started, 0, Result{
			Status: frame.StatusUnknown, Outcome: OutcomeError,
			ErrorClass: frame.ClassInternal, Message: err.Error(),
		})
	}
	defer release()

	g, err := e.Flows.Graph(ctx, m.FlowVersionID)
	if err != nil {
		return e.emit(m, slot, started, 0, Result{
			Status: frame.StatusUnknown, Outcome: OutcomeError,
			ErrorClass: frame.ClassInternal,
			Message:    fmt.Sprintf("flow version %s is unavailable: %v", m.FlowVersionID, err),
		})
	}

	vars := MergeVars(g, d, m)

	var last Result
	var lastCapture json.RawMessage
	attempts := m.Retries + 1

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && m.RetryInterval > 0 {
			select {
			case <-time.After(m.RetryInterval):
			case <-ctx.Done():
				last.Attempt = attempt
				return e.emitResult(m, slot, started, last, lastCapture)
			}
		}

		res, capt := e.attempt(ctx, m, d, g, vars, slot, attempt)
		last, lastCapture = res, capt

		// Retries exist for transient faults. An assertion failure means the
		// device answered and the value was out of range, and re-asking will
		// produce the same answer — retrying it only delays the alert and
		// spends the run's budget.
		if res.Outcome == OutcomeOK || res.ErrorClass == frame.ClassAssertion {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}

	return e.emitResult(m, slot, started, last, lastCapture)
}

// attempt is one whole-flow execution.
func (e *Executor) attempt(ctx context.Context, m Monitor, d Device, g *graph.Graph, vars map[string]any, slot time.Time, attempt int) (Result, json.RawMessage) {
	bounds := e.Bounds
	if m.Timeout > 0 {
		bounds.RunWallClock = m.Timeout
	}
	bounds, _ = bounds.Clamp()

	rc := runtime.NewRunContext(fmt.Sprintf("%s-%d-%d", m.ID, slot.UnixNano(), attempt), slot)
	rc.Attempt = attempt
	rc.Vars = vars

	rec := capture.NewRecorder(bounds.CapturedFrameSize)

	// The dialer rides on the context, which is how a transport reaches the
	// egress policy without the engine knowing transports exist.
	runCtx := ctx
	if e.Dialer != nil {
		runCtx = egress.WithDialer(ctx, e.Dialer)
	}

	res := runtime.Run(runCtx, g, e.Factory, e.Registry, bounds, rc, rec)

	out := Result{Attempt: attempt, Status: res.Status, Outcome: OutcomeOK}
	if res.Err != nil {
		out.Outcome = OutcomeFailed
		out.Message = res.Err.Error()
		out.ErrorClass = classOf(res)
	} else if res.Warning != "" {
		out.Message = res.Warning
	}

	captureJSON, err := json.Marshal(rec.Entries())
	if err != nil {
		// A capture that will not serialise must not fail the run: the result
		// is the load-bearing half and the capture is the diagnostic.
		captureJSON = nil
	}
	return out, captureJSON
}

// classOf extracts the error class from a failed run, so alert routing sees
// `protocol` rather than a generic failure — the distinction between a flow
// bug and a gear fault is the whole point of the classes (spec §11).
func classOf(res *runtime.Result) frame.ErrorClass {
	var f frame.Failure
	if errors.As(res.Err, &f) && f.Class != "" {
		return f.Class
	}
	for _, n := range res.Nodes {
		if n.State == runtime.NodeError && n.Failure != nil && n.Failure.Class != "" {
			return n.Failure.Class
		}
	}
	return frame.ClassInternal
}

func (e *Executor) emit(m Monitor, slot, started time.Time, attempt int, r Result) (Result, error) {
	r.Attempt = attempt
	return e.emitResult(m, slot, started, r, nil)
}

func (e *Executor) emitResult(m Monitor, slot, started time.Time, r Result, capt json.RawMessage) (Result, error) {
	r.MonitorID = m.ID
	r.FlowVersionID = m.FlowVersionID
	r.ScheduledAt = slot
	r.StartedAt = started
	r.DurationMS = time.Since(started).Milliseconds()
	if r.Status == "" {
		r.Status = frame.StatusUnknown
	}
	if r.Outcome == "" {
		r.Outcome = OutcomeError
	}
	if r.ErrorClass == "" {
		r.ErrorClass = frame.ClassNone
	}

	if e.Sink == nil {
		return r, nil
	}
	body, err := json.Marshal(r)
	if err != nil {
		return r, fmt.Errorf("encoding result for monitor %s: %w", m.ID, err)
	}
	// A throttled run carries no capture: nothing was executed, so there is
	// nothing to inspect, and writing an empty one would consume the spool
	// budget that real diagnostics need.
	if r.Outcome == OutcomeThrottled {
		capt = nil
	}
	if _, err := e.Sink.Add(body, capt); err != nil {
		return r, fmt.Errorf("spooling result for monitor %s: %w", m.ID, err)
	}
	return r, nil
}
