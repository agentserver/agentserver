package noise

import "errors"

// PendingResponderHandshake mirrors codex's executor-side state. We
// implement it for unit tests (round-trip the wire against ourselves)
// and for the mock executor in Phase 1.6 bit-compat testing.
// Production gateway code never uses it directly — the gateway is
// always the initiator.
type PendingResponderHandshake struct {
	st               *hybridState
	InitiatorPubKey  PublicKey
	InitiatorPayload []byte
}

// ReadInitiatorRequest parses msg1 with the local responder identity,
// recovering the initiator's pinned static key and the encrypted
// payload (typically the harness_key_authorization HMAC).
func ReadInitiatorRequest(identity *Identity, prologue, msg1 []byte) (*PendingResponderHandshake, error) {
	if len(msg1) > MaxMessageLen {
		return nil, errors.New("noise: msg1 exceeds MAX_MESSAGE_LEN")
	}
	st, err := newHybridState(false, identity, nil, nil, prologue)
	if err != nil {
		return nil, err
	}
	payload, err := st.readMessage(msg1Tokens, msg1)
	if err != nil {
		return nil, err
	}
	pk, err := st.remoteStaticPublicKey()
	if err != nil {
		return nil, err
	}
	return &PendingResponderHandshake{
		st:               st,
		InitiatorPubKey:  pk,
		InitiatorPayload: payload,
	}, nil
}

// Complete writes msg2 (empty payload by protocol) and returns the
// established transport.
func (h *PendingResponderHandshake) Complete() (*Transport, []byte, error) {
	msg2, err := h.st.writeMessage(msg2Tokens, nil)
	if err != nil {
		return nil, nil, err
	}
	if len(msg2) > MaxMessageLen {
		return nil, nil, errors.New("noise: msg2 exceeds MAX_MESSAGE_LEN")
	}
	return h.st.split(), msg2, nil
}
