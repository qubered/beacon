// Command beaconctl is the operator CLI: database migration, agent enrolment
// tokens, Pack lint/sign/verify, fixture replay and offline flow validation.
//
// Fixture replay matters more than it looks: it is what lets someone author a
// Pack at a desk with the gear locked in a venue, and it is how a Pack's flows
// are regression-tested in CI (decision D27).
package main

import (
	"fmt"
	"os"

	"github.com/qubered/beacon/internal/buildinfo"
)

func usage() {
	fmt.Fprintln(os.Stderr, `beaconctl - the Beacon operator CLI

Usage:
  beaconctl --version
  beaconctl flow run --graph <file> [--fixture <file> --root <node-id>] [--timeout <duration>]

flow run executes a flow graph through the real engine and node catalogue.
Until transports land in M2, a root node's input comes from --fixture instead
of a wire — see docs/ROADMAP.md milestone M1.

Everything else (migrate, enrol, pack lint/sign) is not implemented yet.`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--version", "-version":
		fmt.Printf("beaconctl %s\n", buildinfo.String())
	case "flow":
		if err := runFlowCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(1)
	}
}
