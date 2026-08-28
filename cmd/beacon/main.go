// Command beacon is the Core control plane: UI, API, alerting, ingest, storage,
// the agent registry — and a local agent.
//
// Core runs an agent too, and that is a design constraint rather than a
// convenience (decision D13). It forces exactly one execution implementation to
// exist. If Core executed flows one way and agents another, the two would drift
// within a month. Core's local agent gets no special treatment: same enrolment,
// same capability declaration, same link, just over loopback.
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
		fmt.Printf("beacon %s\n", buildinfo.String())
		return
	}

	fmt.Fprintln(os.Stderr, "beacon core: not implemented yet — see docs/ROADMAP.md (milestone M0)")
	os.Exit(1)
}
