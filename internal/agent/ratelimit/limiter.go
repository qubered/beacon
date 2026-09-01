package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrThrottled is returned when a device's token bucket has nothing to give
// within the caller's wait budget.
//
// A throttled monitor is not a failed monitor (spec §8). The executor records
// a throttled outcome and increments a counter; it does not transition the
// monitor towards down, because the device never misbehaved — the schedule
// did.
var ErrThrottled = errors.New("throttled: the device's rate limit was reached")

// Limits are the per-device ceilings.
type Limits struct {
	// MaxConcurrent is the number of simultaneous connections permitted to
	// one device. Spec §8 defaults this to 1 for session-capable devices:
	// plenty of AV gear accepts a second connection and then behaves badly on
	// both.
	MaxConcurrent int

	// MinInterval is the minimum spacing between requests, enforced as a
	// token bucket so a burst is permitted up to Burst and the sustained rate
	// still averages out.
	MinInterval time.Duration

	// Burst is the bucket's capacity. Zero means 1 — no burst allowance,
	// strict spacing.
	Burst int
}

func (l Limits) withDefaults() Limits {
	if l.MaxConcurrent < 1 {
		l.MaxConcurrent = 1
	}
	if l.Burst < 1 {
		l.Burst = 1
	}
	return l
}

// Limiter holds the limits for every device an agent is assigned.
//
// The bucket spans *all* monitors bound to a device, which is the entire
// point: six monitors each politely spacing their own requests still produce
// six times the traffic at the device, and the device is what breaks. Keying
// on the device rather than the monitor is what makes the limit mean anything.
type Limiter struct {
	mu      sync.Mutex
	devices map[string]*deviceLimiter

	throttled map[string]int
}

func New() *Limiter {
	return &Limiter{devices: map[string]*deviceLimiter{}, throttled: map[string]int{}}
}

// Configure sets or replaces one device's limits.
//
// A changed concurrency cap takes effect on the next acquire, in both
// directions — tightening it must actually tighten it, or the number an
// operator set and the number the device experiences drift apart. Leases
// already held stay valid: revoking one mid-request would abort a monitor that
// did nothing wrong, and the cap is reached again within one run either way.
func (l *Limiter) Configure(deviceID string, limits Limits) {
	limits = limits.withDefaults()
	l.mu.Lock()
	defer l.mu.Unlock()
	d, ok := l.devices[deviceID]
	if !ok {
		l.devices[deviceID] = newDeviceLimiter(limits, time.Now())
		return
	}
	d.reconfigure(limits, time.Now())
}

// Acquire takes one concurrency slot and one token for deviceID, blocking up
// to the context's deadline. The returned release must be called when the
// device interaction finishes.
//
// A device with no configured limits is unlimited: an agent that has not yet
// received a device's configuration must keep monitoring rather than refuse,
// since the failure mode of guessing a limit is a monitor that silently stops.
func (l *Limiter) Acquire(ctx context.Context, deviceID string) (release func(), err error) {
	l.mu.Lock()
	d, ok := l.devices[deviceID]
	l.mu.Unlock()
	if !ok {
		return func() {}, nil
	}

	releaseSlot, err := d.acquireSlot(ctx)
	if err != nil {
		l.countThrottle(deviceID)
		return nil, err
	}
	if err := d.waitForToken(ctx); err != nil {
		releaseSlot()
		l.countThrottle(deviceID)
		return nil, err
	}
	return releaseSlot, nil
}

// Throttled reports how many times deviceID has been throttled. A non-zero
// count means someone has over-scheduled the device and needs to know (spec
// §8) — it is surfaced as a metric rather than buried in a log.
func (l *Limiter) Throttled(deviceID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.throttled[deviceID]
}

func (l *Limiter) countThrottle(deviceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.throttled[deviceID]++
}

// deviceLimiter is one device's concurrency semaphore and token bucket.
//
// The concurrency gate counts held leases rather than draining a buffered
// channel, because a channel's capacity is fixed at creation and an operator
// must be able to *lower* a device's cap after discovering it misbehaves on a
// second connection. With a channel, tightening the cap silently kept the old,
// wider one — the limit would read as 1 in the configuration and behave as 4
// at the device, which is the worst shape a safety limit can have.
type deviceLimiter struct {
	mu       sync.Mutex
	released sync.Cond // signalled whenever a lease is returned or the cap rises

	limits   Limits
	held     int
	tokens   float64
	lastFill time.Time
}

func newDeviceLimiter(limits Limits, now time.Time) *deviceLimiter {
	d := &deviceLimiter{limits: limits, tokens: float64(limits.Burst), lastFill: now}
	d.released.L = &d.mu
	return d
}

func (d *deviceLimiter) reconfigure(limits Limits, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.limits = limits
	if d.tokens > float64(limits.Burst) {
		d.tokens = float64(limits.Burst)
	}
	d.lastFill = now
	// Raising the cap must wake anything waiting on the old one. Lowering it
	// takes effect for the next acquire; leases already held stay valid, since
	// revoking one mid-request would abort a monitor that did nothing wrong.
	d.released.Broadcast()
}

// acquireSlot takes one concurrency lease, waiting until one frees up or the
// context gives out.
//
// sync.Cond has no context-aware Wait, so a watchdog goroutine broadcasts on
// cancellation to wake the waiter, which then re-checks ctx.Err itself.
func (d *deviceLimiter) acquireSlot(ctx context.Context) (func(), error) {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.released.Broadcast()
			d.mu.Unlock()
		case <-stop:
		}
	}()

	d.mu.Lock()
	defer d.mu.Unlock()
	for d.held >= d.limits.MaxConcurrent {
		if ctx.Err() != nil {
			return nil, ErrThrottled
		}
		d.released.Wait()
	}
	if ctx.Err() != nil {
		return nil, ErrThrottled
	}
	d.held++

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.held--
			d.released.Broadcast()
			d.mu.Unlock()
		})
	}, nil
}

// waitForToken refills by elapsed time, then either consumes a token or waits
// exactly as long as one takes to arrive.
//
// Waiting rather than failing immediately is deliberate: a monitor arriving
// 40ms early against a 1s minimum interval should run 40ms late, not record a
// throttle. It gives up only when the context — which carries the run
// deadline — says there is no time left, which is the case that genuinely
// means over-scheduled.
func (d *deviceLimiter) waitForToken(ctx context.Context) error {
	for {
		d.mu.Lock()
		now := time.Now()
		if d.limits.MinInterval > 0 {
			elapsed := now.Sub(d.lastFill)
			if elapsed > 0 {
				d.tokens += elapsed.Seconds() / d.limits.MinInterval.Seconds()
				if d.tokens > float64(d.limits.Burst) {
					d.tokens = float64(d.limits.Burst)
				}
			}
		} else {
			d.tokens = float64(d.limits.Burst)
		}
		d.lastFill = now

		if d.tokens >= 1 {
			d.tokens--
			d.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - d.tokens) * float64(d.limits.MinInterval))
		d.mu.Unlock()

		timer := time.NewTimer(need)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ErrThrottled
		}
	}
}
