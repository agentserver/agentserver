package noise

// hybridState is the runtime symmetric/handshake state shared by both
// the initiator and the (test-only) responder. It tracks Noise's
// chaining key + handshake hash + (optional) symmetric key, the
// classical X25519 ephemeral / static / remote-ephemeral / remote-
// static keypairs, and the matching ML-KEM-768 keypairs.
//
// Implementation deferred to Phase 1.3.
type hybridState struct{}

// keyedCounter holds one direction's AES-256-GCM key plus its 64-bit
// little-endian nonce counter. Nonce field of AES-GCM is 12 bytes;
// Noise uses the low 8 bytes as a counter and pads the high 4 bytes
// with zeros (per noise spec §5.1).
type keyedCounter struct {
	key   [AESGCMKeyLen]byte
	nonce uint64
}
