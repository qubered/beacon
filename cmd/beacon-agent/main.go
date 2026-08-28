// Command beacon-agent is a deployed executor: it schedules and runs its
// assigned monitors, holds device sockets, and ships results to Core.
//
// It dials out and never listens (decision D11), it schedules its own work
// against local state (decision D14), and it keeps monitoring while
// disconnected, backfilling on reconnect (invariant I6).
//
// It is never called a node. A node is a box on the flow canvas.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/qubered/beacon/internal/buildinfo"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("beacon-agent %s\n", buildinfo.String())
		return
	}

	fmt.Fprintln(os.Stderr, "beacon agent: not implemented yet — see docs/ROADMAP.md (milestone M0)")
	os.Exit(1)
}
