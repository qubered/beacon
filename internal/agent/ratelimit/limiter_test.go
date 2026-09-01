package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestBucket_IsSharedAcrossMonitorsOnOneDevice is the roadmap's M2 exit gate.
//
// Two monitors on one device must share one bucket. If each got its own, the
// device would see twice the configured rate — which is the exact failure the
// limit exists to prevent, and it would pass any test that only exercised one
// monitor.
func TestBucket_IsSharedAcrossMonitorsOnOneDevice(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 4, MinInterval: 50 * time.Millisecond, Burst: 1})

	ctx := context.Background()
	start := time.Now()

	// One token is in the bucket at rest, so the first Acquire is immediate
	// and the second must wait out a full interval — regardless of which
	// "monitor" makes which call.
	rel1, err := l.Acquire(ctx, "dev-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel1()

	rel2, err := l.Acquire(ctx, "dev-1")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	rel2()

	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("two acquires on one device completed in %s; the bucket is not shared between monitors", elapsed)
	}
}

// TestConcurrency_CapIsEnforced: MaxConcurrent 1 is the default for
// session-capable gear, and plenty of AV devices accept a second connection
// and then misbehave on both.
func TestConcurrency_CapIsEnforced(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 1, Burst: 100})

	rel, err := l.Acquire(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "dev-1"); err != ErrThrottled {
		t.Fatalf("a second concurrent acquire returned %v; want ErrThrottled", err)
	}

	rel()
	if _, err := l.Acquire(context.Background(), "dev-1"); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

// TestConcurrency_TighteningTheCapTakesEffect: an operator who lowers a
// device's cap after discovering it misbehaves on a second connection must
// actually get the lower cap. A limit that reads as 1 in the configuration and
// behaves as 4 at the device is the worst shape a safety limit can have.
func TestConcurrency_TighteningTheCapTakesEffect(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 4, Burst: 100})
	l.Configure("dev-1", Limits{MaxConcurrent: 1, Burst: 100})

	rel, err := l.Acquire(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "dev-1"); err != ErrThrottled {
		t.Fatalf("a second lease was granted after the cap was tightened to 1 (got %v)", err)
	}
}

// TestConcurrency_RaisingTheCapWakesAWaiter: widening a cap must release
// anything already blocked on the old one, rather than leaving it waiting for
// a lease that has already been made available.
func TestConcurrency_RaisingTheCapWakesAWaiter(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 1, Burst: 100})

	rel, err := l.Acquire(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := l.Acquire(ctx, "dev-1")
		if err == nil {
			r()
		}
		got <- err
	}()

	// Give the waiter time to block, then widen the cap.
	time.Sleep(50 * time.Millisecond)
	l.Configure("dev-1", Limits{MaxConcurrent: 2, Burst: 100})

	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the waiter was not released by the wider cap: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("raising the cap did not wake a waiter blocked on the old one")
	}
}

// TestThrottle_IsCountedNotFailed: a throttled outcome is distinct from a
// failure and increments a counter, because a non-zero count means someone
// over-scheduled the device and needs to know (spec §8).
func TestThrottle_IsCountedNotFailed(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 1, Burst: 1})

	rel, _ := l.Acquire(context.Background(), "dev-1")
	defer rel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _ = l.Acquire(ctx, "dev-1")

	if got := l.Throttled("dev-1"); got != 1 {
		t.Fatalf("throttle count is %d, want 1", got)
	}
	if got := l.Throttled("dev-2"); got != 0 {
		t.Fatalf("an unrelated device has throttle count %d; counters must be per-device", got)
	}
}

// TestAcquire_ReleasesTheSlotWhenTheTokenWaitTimesOut. Acquiring the
// concurrency slot and then giving up on the bucket must not leak the slot —
// otherwise a device that throttles once is unreachable until the agent
// restarts.
func TestAcquire_ReleasesTheSlotWhenTheTokenWaitTimesOut(t *testing.T) {
	l := New()
	l.Configure("dev-1", Limits{MaxConcurrent: 1, MinInterval: time.Hour, Burst: 1})

	rel, err := l.Acquire(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel()

	// The bucket is now empty and refills once an hour, so this must throttle.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "dev-1"); err != ErrThrottled {
		t.Fatalf("want ErrThrottled, got %v", err)
	}

	// If the slot leaked, this blocks on the semaphore instead of the bucket.
	// Configuring a fresh device proves the distinction cleanly.
	l.Configure("dev-2", Limits{MaxConcurrent: 1, Burst: 1})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, err := l.Acquire(context.Background(), "dev-2")
		if err == nil {
			r()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("acquire on a fresh device blocked; a concurrency slot leaked")
	}
}

// TestAcquire_UnconfiguredDeviceIsUnlimited: an agent that has not yet
// received a device's configuration must keep monitoring. Guessing a limit
// produces a monitor that silently stops, which is worse than no limit.
func TestAcquire_UnconfiguredDeviceIsUnlimited(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := l.Acquire(context.Background(), "never-configured")
			if err != nil {
				t.Errorf("unconfigured device was limited: %v", err)
				return
			}
			rel()
		}()
	}
	wg.Wait()
}
