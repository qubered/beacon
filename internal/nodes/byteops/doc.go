// Package byteops holds the byte composition and encoding nodes.
//
// Spec §6.4: Build Bytes (with length and checksum placeholders resolved after the body is assembled), Encode/Decode, Checksum, HMAC, Split Frames, Slice, Bit Field, Endian Swap.
//
// Build Bytes is what makes binary protocols authorable in the same builder as ASCII ones (decision D4). Checksum is parameterised over polynomial, init, reflection and xor-out because every vendor picked a different CRC-16.
package byteops
