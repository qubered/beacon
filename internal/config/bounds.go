package config

import (
	"fmt"
	"time"
)

// Bounds are the execution limits from spec §6.2.
//
// Every value has a default and a ceiling. The default is a suggestion an
// operator may lower or raise; the ceiling is enforced and an operator cannot
// configure past it. That split is the point: a flow author cannot talk their
// way out of termination, and an operator cannot accidentally disable it.
type Bounds struct {
	RunWallClock       time.Duration
	NodeExecutions     int
	LoopIterations     int
	ConcurrentBranches int
	FrameSize          int64
	CapturedFrameSize  int64
	SandboxMemory      int64
	SandboxCPU         time.Duration
}

// Ceilings are hard maxima. Nothing in the system may exceed them.
var Ceilings = Bounds{
	RunWallClock:       300 * time.Second,
	NodeExecutions:     5000,
	LoopIterations:     200,
	ConcurrentBranches: 32,
	FrameSize:          32 << 20,
	CapturedFrameSize:  64 << 10, // captures truncate rather than fail
	SandboxMemory:      128 << 20,
	SandboxCPU:         250 * time.Millisecond,
}

// DefaultBounds are the suggested starting values.
func DefaultBounds() Bounds {
	return Bounds{
		RunWallClock:       30 * time.Second,
		NodeExecutions:     500,
		LoopIterations:     200,
		ConcurrentBranches: 8,
		FrameSize:          4 << 20,
		CapturedFrameSize:  64 << 10,
		SandboxMemory:      16 << 20,
		SandboxCPU:         250 * time.Millisecond,
	}
}

// Clamp lowers any value that exceeds its ceiling and raises any non-positive
// value to the default, returning the applied bounds and what was adjusted.
//
// Clamping rather than rejecting is deliberate: a monitor whose configuration
// drifted past a ceiling should keep running under the ceiling, not stop. The
// adjustments are returned so the caller can surface them.
func (b Bounds) Clamp() (Bounds, []string) {
	var notes []string
	d := DefaultBounds()

	clampDur := func(name string, v, def, max time.Duration) time.Duration {
		if v <= 0 {
			return def
		}
		if v > max {
			notes = append(notes, fmt.Sprintf("%s %s exceeds the ceiling %s and was clamped", name, v, max))
			return max
		}
		return v
	}
	clampInt := func(name string, v, def, max int) int {
		if v <= 0 {
			return def
		}
		if v > max {
			notes = append(notes, fmt.Sprintf("%s %d exceeds the ceiling %d and was clamped", name, v, max))
			return max
		}
		return v
	}
	clampSize := func(name string, v, def, max int64) int64 {
		if v <= 0 {
			return def
		}
		if v > max {
			notes = append(notes, fmt.Sprintf("%s %d exceeds the ceiling %d and was clamped", name, v, max))
			return max
		}
		return v
	}

	return Bounds{
		RunWallClock:       clampDur("run wall clock", b.RunWallClock, d.RunWallClock, Ceilings.RunWallClock),
		NodeExecutions:     clampInt("node executions", b.NodeExecutions, d.NodeExecutions, Ceilings.NodeExecutions),
		LoopIterations:     clampInt("loop iterations", b.LoopIterations, d.LoopIterations, Ceilings.LoopIterations),
		ConcurrentBranches: clampInt("concurrent branches", b.ConcurrentBranches, d.ConcurrentBranches, Ceilings.ConcurrentBranches),
		FrameSize:          clampSize("frame size", b.FrameSize, d.FrameSize, Ceilings.FrameSize),
		CapturedFrameSize:  clampSize("captured frame size", b.CapturedFrameSize, d.CapturedFrameSize, Ceilings.CapturedFrameSize),
		SandboxMemory:      clampSize("sandbox memory", b.SandboxMemory, d.SandboxMemory, Ceilings.SandboxMemory),
		SandboxCPU:         clampDur("sandbox CPU", b.SandboxCPU, d.SandboxCPU, Ceilings.SandboxCPU),
	}, notes
}
