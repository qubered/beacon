// Package validate implements the publish-time gates.
//
// Spec §15. Publishing is blocked on: no reachable Emit Status node, undeclared metric labels, a loop without an iteration cap, worst-case run duration exceeding the monitor interval, a sweep outside the flow's declared egress scope, or a required capability that no assigned agent declares.
//
// Worst-case run duration is (retries+1)*timeout + retries*retry_interval (spec §6.2).
package validate
