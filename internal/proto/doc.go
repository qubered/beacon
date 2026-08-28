// Package proto defines the binary message format spoken over the Core<->Agent link.
//
// Spec §7.2. Binary, not text: run captures are raw device bytes and text-encoding them inflates the largest thing on the wire by a third.
//
// Owns version negotiation. Agents older than Core are supported within a stated window; agents newer than Core are refused (spec §7.5).
package proto
