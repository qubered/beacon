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
  beaconctl migrate [--database-url <url>] [--dir migrations] [--dry-run]
  beaconctl flow run --graph <file> [--fixture <file> --root <node-id>]
                     [--allow CIDR,proto[,ports]]... [--allow-loopback]
                     [--timeout <duration>]

flow run executes a flow graph through the real engine and node catalogue.
Bytes reach it either from --fixture, replaying a recorded device response, or
from a transport node opening a real socket.

Transports are subject to the same egress policy an agent enforces, and it is
default-deny: without at least one --allow rule every connection is refused.

  --allow 10.0.0.0/8,tcp,1-65535     a subnet, TCP, any port
  --allow 192.168.1.0/24,icmp        a subnet, ping
  --allow-loopback                   reach a simulator on this host

migrate applies the numbered SQL migrations, forward-only, each in its own
transaction with the row recording it. It is a separate operator action rather
than something Core does at startup.

Everything else (enrol, pack lint/sign) is not implemented yet.`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--version", "-version":
		fmt.Printf("beaconctl %s\n", buildinfo.String())
	case "migrate":
		if err := runMigrateCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
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
