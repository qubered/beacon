// Package discovery is the candidate review queue, reachability triage and Pack device-profile matching.
//
// Spec §13. Discovery is not a special subsystem: it is a flow that emits device candidates. This package owns what happens after the emission.
//
// Candidates land in a review queue with accept/edit/ignore and a diff since the last scan. "Missing" is an event, never a delete.
//
// Reachability triage on accept records reachable, unreachable-from-here (present in the switch ARP table, silent to us) or filtered (refused rather than timed out, which usually means an ACL). Only the first can carry monitors; the others are still worth listing, because "there is a device on port 14 we cannot see" is itself useful.
package discovery
