// Package buildinfo carries the build identity reported to operators and
// declared to Core on the link.
//
// Version is part of the capability set (spec §7.5): agents older than Core are
// supported within a stated window, agents newer than Core are refused.
package buildinfo

import "fmt"

var (
	Version = "0.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string { return fmt.Sprintf("%s (%s, built %s)", Version, Commit, Date) }
