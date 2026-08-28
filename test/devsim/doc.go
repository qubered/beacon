// Package devsim provides fake devices for tests: raw-ASCII TCP with IAC noise and a banner, a challenge-response responder, an SNMP agent, a GraphQL endpoint and a push source.
//
// These back the acceptance scenarios in spec §18 without any gear present, and they are the reason CI can falsify the design rather than merely exercise it.
package devsim
