package noise

import "errors"

// Transport carries application traffic after the handshake completes.
// Each call to Encrypt or Decrypt advances the nonce for that
// direction. Out-of-order or replayed frames fail the AEAD check
// because the nonce will not match.
//
// Transport does not implement automatic rekey. Per noise §11.3,
// rekeying is a higher-level concern; codex's relay assumes one
// session per harness bridge (<<2^32 records).
type Transport struct {
	send keyedCounter
	recv keyedCounter
}

func (t *Transport) Encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext)+AESGCMTagLen > MaxMessageLen {
		return nil, errors.New("noise: transport plaintext too large")
	}
	return t.send.seal(plaintext)
}

func (t *Transport) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) > MaxMessageLen {
		return nil, errors.New("noise: transport ciphertext too large")
	}
	return t.recv.open(ciphertext)
}
