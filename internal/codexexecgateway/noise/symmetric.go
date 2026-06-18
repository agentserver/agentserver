package noise

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// symmetricState implements the Noise SymmetricState object from
// noise spec §5.2 with SHA-256 + AES-256-GCM. It is the cryptographic
// core driving both handshake mixing and transport-key derivation.
type symmetricState struct {
	ck     [SHA256HashLen]byte // chaining key
	h      [SHA256HashLen]byte // handshake hash
	hasKey bool
	k      [AESGCMKeyLen]byte // symmetric key (valid when hasKey)
	n      uint64             // nonce counter (valid when hasKey)
}

func initializeSymmetric(protocolName string) symmetricState {
	var s symmetricState
	name := []byte(protocolName)
	if len(name) <= SHA256HashLen {
		copy(s.h[:], name)
		// remainder of s.h is already zero
	} else {
		s.h = sha256.Sum256(name)
	}
	s.ck = s.h
	return s
}

func (s *symmetricState) mixHash(data []byte) {
	hasher := sha256.New()
	hasher.Write(s.h[:])
	hasher.Write(data)
	hasher.Sum(s.h[:0])
}

func (s *symmetricState) mixKey(ikm []byte) {
	ck, tempK := hkdf2(s.ck[:], ikm)
	s.ck = ck
	s.k = tempK
	s.hasKey = true
	s.n = 0
}

func (s *symmetricState) mixKeyAndHash(ikm []byte) {
	ck, tempH, tempK := hkdf3(s.ck[:], ikm)
	s.ck = ck
	s.mixHash(tempH[:])
	s.k = tempK
	s.hasKey = true
	s.n = 0
}

// encryptAndHash either AEAD-seals plaintext under the current key
// (using h as associated data) or, when no key is active, passes
// plaintext through unchanged. The resulting bytes are always mixed
// into the running handshake hash.
func (s *symmetricState) encryptAndHash(plaintext []byte) ([]byte, error) {
	var out []byte
	if s.hasKey {
		aead, err := s.aead()
		if err != nil {
			return nil, err
		}
		nonce := noiseNonce(s.n)
		out = aead.Seal(nil, nonce[:], plaintext, s.h[:])
		s.n++
	} else {
		out = append([]byte(nil), plaintext...)
	}
	s.mixHash(out)
	return out, nil
}

// decryptAndHash is the inverse of encryptAndHash. Per noise spec the
// hash is mixed with the CIPHERTEXT (before decryption) so both peers
// arrive at the same h regardless of decrypt success.
func (s *symmetricState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	hashInput := append([]byte(nil), ciphertext...)
	var plaintext []byte
	if s.hasKey {
		aead, err := s.aead()
		if err != nil {
			return nil, err
		}
		nonce := noiseNonce(s.n)
		plaintext, err = aead.Open(nil, nonce[:], ciphertext, s.h[:])
		if err != nil {
			return nil, fmt.Errorf("noise: AEAD open failed: %w", err)
		}
		s.n++
	} else {
		plaintext = append([]byte(nil), ciphertext...)
	}
	s.mixHash(hashInput)
	return plaintext, nil
}

// split derives the two transport keys at handshake completion.
// Returned (k1, k2) where k1 is initiator→responder and k2 is
// responder→initiator (noise spec §5.2).
func (s *symmetricState) split() ([AESGCMKeyLen]byte, [AESGCMKeyLen]byte) {
	k1, k2 := hkdf2(s.ck[:], nil)
	return k1, k2
}

func (s *symmetricState) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.k[:])
	if err != nil {
		return nil, fmt.Errorf("noise: aes init: %w", err)
	}
	return cipher.NewGCM(block)
}

// noiseNonce builds the 12-byte AES-GCM nonce per noise spec §5.1:
// 4 zero bytes followed by the 64-bit counter in little-endian.
func noiseNonce(n uint64) [AESGCMNonceLen]byte {
	var nonce [AESGCMNonceLen]byte
	binary.LittleEndian.PutUint64(nonce[4:], n)
	return nonce
}

// hkdf2 implements the 2-output variant of noise's HKDF (§4.3).
// Equivalent to RFC 5869 HKDF-Extract+Expand with empty info, salt=ck,
// ikm=ikm, expanding to 64 bytes split in half.
func hkdf2(ck, ikm []byte) (out1, out2 [SHA256HashLen]byte) {
	tempKey := hmacSHA256(ck, ikm)
	o1 := hmacSHA256(tempKey[:], []byte{0x01})
	o2Input := append(append([]byte(nil), o1[:]...), 0x02)
	o2 := hmacSHA256(tempKey[:], o2Input)
	out1, out2 = o1, o2
	return
}

func hkdf3(ck, ikm []byte) (out1, out2, out3 [SHA256HashLen]byte) {
	tempKey := hmacSHA256(ck, ikm)
	o1 := hmacSHA256(tempKey[:], []byte{0x01})
	o2Input := append(append([]byte(nil), o1[:]...), 0x02)
	o2 := hmacSHA256(tempKey[:], o2Input)
	o3Input := append(append([]byte(nil), o2[:]...), 0x03)
	o3 := hmacSHA256(tempKey[:], o3Input)
	out1, out2, out3 = o1, o2, o3
	return
}

func hmacSHA256(key, data []byte) [SHA256HashLen]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	var out [SHA256HashLen]byte
	mac.Sum(out[:0])
	return out
}

// keyedCounter is one direction of an established transport channel:
// AES-256-GCM key plus a monotonically-increasing 64-bit nonce
// counter. Replay or out-of-order frames fail the AEAD check.
type keyedCounter struct {
	key   [AESGCMKeyLen]byte
	nonce uint64
}

func (kc *keyedCounter) seal(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(kc.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := noiseNonce(kc.nonce)
	out := aead.Seal(nil, nonce[:], plaintext, nil)
	kc.nonce++
	return out, nil
}

func (kc *keyedCounter) open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < AESGCMTagLen {
		return nil, errors.New("noise: ciphertext shorter than tag")
	}
	block, err := aes.NewCipher(kc.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := noiseNonce(kc.nonce)
	plaintext, err := aead.Open(nil, nonce[:], ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("noise: transport AEAD open failed: %w", err)
	}
	kc.nonce++
	return plaintext, nil
}
