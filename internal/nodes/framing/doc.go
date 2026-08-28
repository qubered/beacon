// Package framing implements the read-until strategies shared by TCP Request, Expect and Split Frames.
//
// Spec §6.4. This is the framing engine and it is what unlocks the long tail: delimiter, length prefix, fixed, regex, start/end markers, quiet period, until close, message count. Per-read options: strip IAC sequences, max bytes, and discard-before.
//
// Deliberate omission (D8): there is no Telnet node. Genuine Telnet is a preset of TCP Request with IAC stripping and a regex read.
package framing
