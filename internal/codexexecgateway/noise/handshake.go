package noise

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"errors"
	"fmt"
)

// token enumerates the noise tokens our hybrid IK pattern needs. We
// deliberately stay narrow — full pattern parsing would be dead code.
type token int

const (
	tokE token = iota
	tokS
	tokEE
	tokES
	tokSE
	tokSS
	tokEkem
	tokSkem
)

var (
	msg1Tokens = []token{tokSkem, tokE, tokES, tokS, tokSS}
	msg2Tokens = []token{tokEkem, tokSkem, tokE, tokEE, tokSE}
)

// hybridState holds the runtime cryptographic state of one peer's
// half-handshake. The lifecycle is: new → write+read messages in the
// pattern order → split into Transport.
type hybridState struct {
	initiator bool
	ss        symmetricState

	// Local static identity (set by both peers).
	s *Identity

	// Remote static pubkeys. Initiator pins these before start; responder
	// learns them by reading the `S` token in msg1.
	rs    *ecdh.PublicKey
	rsKem *mlkem.EncapsulationKey768

	// Local ephemerals (generated on first `E` write).
	e    *ecdh.PrivateKey
	eKem *mlkem.DecapsulationKey768

	// Remote ephemerals (learned on first `E` read).
	re    *ecdh.PublicKey
	reKem *mlkem.EncapsulationKey768
}

// newHybridState builds either an initiator or responder ready to run
// the IK message pattern. `rs` and `rsKem` are required only on the
// initiator side — the responder learns them from msg1.
func newHybridState(initiator bool, id *Identity, rs *ecdh.PublicKey, rsKem *mlkem.EncapsulationKey768, prologue []byte) (*hybridState, error) {
	if id == nil {
		return nil, errors.New("noise: identity required")
	}
	if initiator && (rs == nil || rsKem == nil) {
		return nil, errors.New("noise: initiator requires remote static keys")
	}
	st := &hybridState{
		initiator: initiator,
		s:         id,
		rs:        rs,
		rsKem:     rsKem,
	}
	st.ss = initializeSymmetric(NoiseProtocolName)
	st.ss.mixHash(prologue)

	// IK pre-message: responder's static keys are pinned by both peers
	// (initiator received them out-of-band; responder owns them).
	var rsDHBytes, rsKemBytes []byte
	if initiator {
		rsDHBytes = rs.Bytes()
		rsKemBytes = rsKem.Bytes()
	} else {
		rsDHBytes = id.dhPubRaw()
		rsKemBytes = id.kemPubRaw()
	}
	st.ss.mixHash(rsDHBytes)
	st.ss.mixHash(rsKemBytes)
	return st, nil
}

// writeMessage runs the given token list as the writer, appending wire
// bytes for each token, then encrypts the supplied payload into the
// final segment.
func (st *hybridState) writeMessage(tokens []token, payload []byte) ([]byte, error) {
	var out []byte
	for _, t := range tokens {
		seg, err := st.writeToken(t)
		if err != nil {
			return nil, err
		}
		out = append(out, seg...)
	}
	enc, err := st.ss.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}
	out = append(out, enc...)
	return out, nil
}

// readMessage consumes a wire message as the reader, parsing each
// token, then decrypts the trailing payload segment.
func (st *hybridState) readMessage(tokens []token, msg []byte) ([]byte, error) {
	cur := 0
	for _, t := range tokens {
		consumed, err := st.readToken(t, msg[cur:])
		if err != nil {
			return nil, err
		}
		cur += consumed
	}
	payload, err := st.ss.decryptAndHash(msg[cur:])
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// writeToken returns the wire bytes the current token emits.
func (st *hybridState) writeToken(t token) ([]byte, error) {
	switch t {
	case tokE:
		if st.e == nil {
			k, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				return nil, fmt.Errorf("noise: X25519 ephemeral: %w", err)
			}
			st.e = k
		}
		if st.eKem == nil {
			k, err := mlkem.GenerateKey768()
			if err != nil {
				return nil, fmt.Errorf("noise: ML-KEM-768 ephemeral: %w", err)
			}
			st.eKem = k
		}
		ePub := st.e.PublicKey().Bytes()
		eKemPub := st.eKem.EncapsulationKey().Bytes()
		st.ss.mixHash(ePub)
		st.ss.mixHash(eKemPub)
		out := make([]byte, 0, X25519PubLen+MLKEM768PubLen)
		out = append(out, ePub...)
		out = append(out, eKemPub...)
		return out, nil

	case tokS:
		dhEnc, err := st.ss.encryptAndHash(st.s.dhPubRaw())
		if err != nil {
			return nil, err
		}
		kemEnc, err := st.ss.encryptAndHash(st.s.kemPubRaw())
		if err != nil {
			return nil, err
		}
		return append(dhEnc, kemEnc...), nil

	case tokEE, tokES, tokSE, tokSS:
		share, err := st.dhShare(t)
		if err != nil {
			return nil, err
		}
		st.ss.mixKey(share)
		return nil, nil

	case tokEkem:
		if st.reKem == nil {
			return nil, errors.New("noise: Ekem write requires peer ephemeral KEM")
		}
		ss, ct := st.reKem.Encapsulate()
		st.ss.mixHash(ct)
		st.ss.mixKey(ss)
		return ct, nil

	case tokSkem:
		var rsKem *mlkem.EncapsulationKey768
		if st.initiator {
			rsKem = st.rsKem
		} else {
			rsKem = st.rsKem
		}
		if rsKem == nil {
			return nil, errors.New("noise: Skem write requires peer static KEM")
		}
		ss, ct := rsKem.Encapsulate()
		enc, err := st.ss.encryptAndHash(ct)
		if err != nil {
			return nil, err
		}
		st.ss.mixKeyAndHash(ss)
		return enc, nil
	}
	return nil, fmt.Errorf("noise: unhandled write token %d", t)
}

// readToken consumes wire bytes from msg starting at offset 0,
// returning the number of bytes consumed.
func (st *hybridState) readToken(t token, msg []byte) (int, error) {
	switch t {
	case tokE:
		if len(msg) < X25519PubLen+MLKEM768PubLen {
			return 0, errors.New("noise: short E token")
		}
		dhBytes := msg[:X25519PubLen]
		kemBytes := msg[X25519PubLen : X25519PubLen+MLKEM768PubLen]
		dh, err := ecdh.X25519().NewPublicKey(dhBytes)
		if err != nil {
			return 0, fmt.Errorf("noise: parse remote E DH: %w", err)
		}
		kem, err := mlkem.NewEncapsulationKey768(kemBytes)
		if err != nil {
			return 0, fmt.Errorf("noise: parse remote E KEM: %w", err)
		}
		st.re = dh
		st.reKem = kem
		st.ss.mixHash(dhBytes)
		st.ss.mixHash(kemBytes)
		return X25519PubLen + MLKEM768PubLen, nil

	case tokS:
		dhLen := X25519PubLen
		if st.ss.hasKey {
			dhLen += AESGCMTagLen
		}
		if len(msg) < dhLen {
			return 0, errors.New("noise: short S DH token")
		}
		dhPlain, err := st.ss.decryptAndHash(msg[:dhLen])
		if err != nil {
			return 0, fmt.Errorf("noise: decrypt remote static DH: %w", err)
		}
		if len(dhPlain) != X25519PubLen {
			return 0, errors.New("noise: remote static DH wrong length")
		}
		dh, err := ecdh.X25519().NewPublicKey(dhPlain)
		if err != nil {
			return 0, fmt.Errorf("noise: parse remote static DH: %w", err)
		}
		st.rs = dh

		kemLen := MLKEM768PubLen
		if st.ss.hasKey {
			kemLen += AESGCMTagLen
		}
		if len(msg[dhLen:]) < kemLen {
			return 0, errors.New("noise: short S KEM token")
		}
		kemPlain, err := st.ss.decryptAndHash(msg[dhLen : dhLen+kemLen])
		if err != nil {
			return 0, fmt.Errorf("noise: decrypt remote static KEM: %w", err)
		}
		if len(kemPlain) != MLKEM768PubLen {
			return 0, errors.New("noise: remote static KEM wrong length")
		}
		kem, err := mlkem.NewEncapsulationKey768(kemPlain)
		if err != nil {
			return 0, fmt.Errorf("noise: parse remote static KEM: %w", err)
		}
		st.rsKem = kem
		return dhLen + kemLen, nil

	case tokEE, tokES, tokSE, tokSS:
		share, err := st.dhShare(t)
		if err != nil {
			return 0, err
		}
		st.ss.mixKey(share)
		return 0, nil

	case tokEkem:
		if len(msg) < MLKEM768CiphertextLen {
			return 0, errors.New("noise: short Ekem ct")
		}
		ct := msg[:MLKEM768CiphertextLen]
		st.ss.mixHash(ct)
		if st.eKem == nil {
			return 0, errors.New("noise: Ekem read requires local ephemeral KEM")
		}
		share, err := st.eKem.Decapsulate(ct)
		if err != nil {
			return 0, fmt.Errorf("noise: Ekem decapsulate: %w", err)
		}
		st.ss.mixKey(share)
		return MLKEM768CiphertextLen, nil

	case tokSkem:
		ctLen := MLKEM768CiphertextLen
		if st.ss.hasKey {
			ctLen += AESGCMTagLen
		}
		if len(msg) < ctLen {
			return 0, errors.New("noise: short Skem ct")
		}
		ct, err := st.ss.decryptAndHash(msg[:ctLen])
		if err != nil {
			return 0, fmt.Errorf("noise: decrypt Skem ct: %w", err)
		}
		if len(ct) != MLKEM768CiphertextLen {
			return 0, errors.New("noise: Skem ct wrong length")
		}
		share, err := st.s.kem.Decapsulate(ct)
		if err != nil {
			return 0, fmt.Errorf("noise: Skem decapsulate: %w", err)
		}
		st.ss.mixKeyAndHash(share)
		return ctLen, nil
	}
	return 0, fmt.Errorf("noise: unhandled read token %d", t)
}

// dhShare resolves which keypair pair the given DH token wants and
// performs the X25519 ECDH. Direction depends on initiator/responder
// role per noise spec.
func (st *hybridState) dhShare(t token) ([]byte, error) {
	var priv *ecdh.PrivateKey
	var pub *ecdh.PublicKey
	switch t {
	case tokEE:
		priv, pub = st.e, st.re
	case tokSS:
		priv, pub = st.s.dh, st.rs
	case tokES:
		if st.initiator {
			priv, pub = st.e, st.rs
		} else {
			priv, pub = st.s.dh, st.re
		}
	case tokSE:
		if st.initiator {
			priv, pub = st.s.dh, st.re
		} else {
			priv, pub = st.e, st.rs
		}
	default:
		return nil, fmt.Errorf("noise: not a DH token: %d", t)
	}
	if priv == nil || pub == nil {
		return nil, fmt.Errorf("noise: DH token %d missing key material", t)
	}
	share, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("noise: ECDH failed: %w", err)
	}
	return share, nil
}

// split derives the two transport keys at handshake completion.
// Initiator: first key seals outbound, second opens inbound. Responder
// mirrors.
func (st *hybridState) split() *Transport {
	k1, k2 := st.ss.split()
	if st.initiator {
		return &Transport{
			send: keyedCounter{key: k1},
			recv: keyedCounter{key: k2},
		}
	}
	return &Transport{
		send: keyedCounter{key: k2},
		recv: keyedCounter{key: k1},
	}
}

// remoteStaticPublicKey serializes the discovered initiator public key
// in the same wire shape as Identity.PublicKey() for the responder's
// authorization handoff.
func (st *hybridState) remoteStaticPublicKey() (PublicKey, error) {
	if st.rs == nil || st.rsKem == nil {
		return PublicKey{}, errors.New("noise: remote static keys not learned yet")
	}
	dhBytes := st.rs.Bytes()
	kemBytes := st.rsKem.Bytes()
	return PublicKey{
		Suite:             SuiteName,
		X25519PublicKey:   stdBase64Encode(dhBytes),
		MLKEM768PublicKey: stdBase64Encode(kemBytes),
	}, nil
}

// InitiatorHandshake drives the harness/gateway side of the hybrid IK
// handshake. Lifecycle: StartInitiator → Finish → Transport.
type InitiatorHandshake struct {
	st *hybridState
}

// StartInitiator generates msg1, encrypting `payload` (typically the
// harness_key_authorization HMAC) into the final ciphertext segment.
// The remote responder public key is pinned for the duration of the
// session.
func StartInitiator(identity *Identity, responder PublicKey, prologue, payload []byte) (*InitiatorHandshake, []byte, error) {
	rsDH, rsKem, err := responder.Decode()
	if err != nil {
		return nil, nil, err
	}
	st, err := newHybridState(true, identity, rsDH, rsKem, prologue)
	if err != nil {
		return nil, nil, err
	}
	msg1, err := st.writeMessage(msg1Tokens, payload)
	if err != nil {
		return nil, nil, err
	}
	if len(msg1) > MaxMessageLen {
		return nil, nil, errors.New("noise: handshake msg1 exceeds MAX_MESSAGE_LEN")
	}
	return &InitiatorHandshake{st: st}, msg1, nil
}

// Finish consumes the responder's msg2 and returns the established
// transport. v1 mandates msg2 carries no payload bytes after the
// AES-GCM tag.
func (hs *InitiatorHandshake) Finish(msg2 []byte) (*Transport, error) {
	if len(msg2) > MaxMessageLen {
		return nil, errors.New("noise: msg2 exceeds MAX_MESSAGE_LEN")
	}
	payload, err := hs.st.readMessage(msg2Tokens, msg2)
	if err != nil {
		return nil, err
	}
	if len(payload) != 0 {
		return nil, errors.New("noise: handshake response payload must be empty")
	}
	return hs.st.split(), nil
}
