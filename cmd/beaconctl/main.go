// Command beaconctl is the operator CLI: database migration, agent enrolment
// tokens, Pack lint/sign/verify, fixture replay and offline flow validation.
//
// Fixture replay matters more than it looks: it is what lets someone author a
// Pack at a desk with the gear locked in a venue, and it is how a Pack's flows
// are regression-tested in CI (decision D27).
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
		fmt.Printf("beaconctl %s\n", buildinfo.String())
		return
	}

	fmt.Fprintln(os.Stderr, "beaconctl: not implemented yet — see docs/ROADMAP.md (milestone M0)")
	os.Exit(1)
}
