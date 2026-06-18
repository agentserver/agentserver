package noise

import "errors"

// PendingResponderHandshake mirrors codex's executor-side state. We
// implement the responder ONLY for unit tests (round-trip the wire
// against ourselves) and for the mock executor used in Phase 1.6
// bit-compat testing. Production gateway code never uses it.
type PendingResponderHandshake struct {
	state            *hybridState
	InitiatorPubKey  PublicKey
	InitiatorPayload []byte
}

func ReadInitiatorRequest(_ *Identity, _ []byte, _ []byte) (*PendingResponderHandshake, error) {
	return nil, errors.New("noise: responder handshake not yet implemented (Phase 1.3, test-only)")
}

// Complete writes msg2 (empty payload) and returns the transport.
func (h *PendingResponderHandshake) Complete() (*Transport, []byte, error) {
	return nil, nil, errors.New("noise: responder complete not yet implemented (Phase 1.3, test-only)")
}
