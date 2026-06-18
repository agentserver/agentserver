package noise

import "errors"

// Transport carries application traffic after the handshake completes.
// Each call to Encrypt or Decrypt advances the implicit nonce for that
// direction. The caller is responsible for ordering ciphertexts before
// passing them to Decrypt — gaps or reorders are detected as auth-tag
// failures because the nonce will not match.
type Transport struct {
	// send and recv hold AES-GCM AEADs derived from the handshake
	// chaining key plus 64-bit little-endian counters.
	send keyedCounter
	recv keyedCounter
}

// Encrypt seals plaintext with the outbound key + next nonce. Output
// is plaintext.len + AESGCMTagLen bytes.
func (t *Transport) Encrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("noise: transport encrypt not yet implemented (Phase 1.4)")
}

// Decrypt opens ciphertext with the inbound key + next nonce.
func (t *Transport) Decrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("noise: transport decrypt not yet implemented (Phase 1.4)")
}
