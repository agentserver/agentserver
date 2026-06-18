package noise

import "errors"

// InitiatorHandshake drives the harness/gateway side of the hybrid IK
// handshake. The gateway is always the initiator — the executor (codex
// exec-server) is the responder.
//
// Lifecycle:
//
//	hs, msg1, err := StartInitiator(identity, responder, prologue, payload)
//	// send msg1 over the relay; receive msg2 from executor
//	transport, err := hs.Finish(msg2)
//	// hs is consumed; use transport for AES-GCM application traffic
type InitiatorHandshake struct {
	state *hybridState
}

// StartInitiator generates the IK msg1 bytes carrying the encrypted
// harness_key_authorization payload. The returned handshake state
// must be passed to Finish exactly once.
func StartInitiator(_ *Identity, _ PublicKey, _ []byte, _ []byte) (*InitiatorHandshake, []byte, error) {
	return nil, nil, errors.New("noise: initiator handshake not yet implemented (Phase 1.3)")
}

// Finish consumes the responder's msg2 and returns the established
// transport. The v1 protocol mandates an empty response payload —
// any bytes after the AES-GCM tag fail the handshake.
func (hs *InitiatorHandshake) Finish(_ []byte) (*Transport, error) {
	return nil, errors.New("noise: initiator handshake finish not yet implemented (Phase 1.3)")
}
