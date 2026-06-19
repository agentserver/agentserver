package noise

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// PublicKey is the wire form of a peer's static identity, matching
// codex's NoiseChannelPublicKey JSON (suite tag + base64 components).
type PublicKey struct {
	Suite             string `json:"suite"`
	X25519PublicKey   string `json:"x25519_public_key"`
	MLKEM768PublicKey string `json:"mlkem768_public_key"`
}

// Identity holds a process's long-lived static Noise keys (both DH and
// KEM components). The two halves are bound by a single PublicKey
// suite tag so cross-suite reuse is rejected.
type Identity struct {
	dh  *ecdh.PrivateKey
	kem *mlkem.DecapsulationKey768
}

func GenerateIdentity() (*Identity, error) {
	curve := ecdh.X25519()
	dh, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("x25519 keygen: %w", err)
	}
	kem, err := mlkem.GenerateKey768()
	if err != nil {
		return nil, fmt.Errorf("mlkem768 keygen: %w", err)
	}
	return &Identity{dh: dh, kem: kem}, nil
}

func (id *Identity) PublicKey() PublicKey {
	return PublicKey{
		Suite:             SuiteName,
		X25519PublicKey:   base64.StdEncoding.EncodeToString(id.dh.PublicKey().Bytes()),
		MLKEM768PublicKey: base64.StdEncoding.EncodeToString(id.kem.EncapsulationKey().Bytes()),
	}
}

func (id *Identity) dhRaw() []byte {
	return id.dh.Bytes()
}

func (id *Identity) dhPubRaw() []byte {
	return id.dh.PublicKey().Bytes()
}

func (id *Identity) kemPubRaw() []byte {
	return id.kem.EncapsulationKey().Bytes()
}

// MarshalJSON delegates to the wire PublicKey shape via its struct tags.
func (k PublicKey) MarshalJSON() ([]byte, error) {
	type alias PublicKey
	return json.Marshal(alias(k))
}

func stdBase64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// Decode parses a wire PublicKey into typed key material. Suite tag is
// validated before any byte length check so a wrong-suite key fails
// loud rather than passing a length check coincidentally.
func (k PublicKey) Decode() (dhPub *ecdh.PublicKey, kemPub *mlkem.EncapsulationKey768, err error) {
	if k.Suite != SuiteName {
		return nil, nil, fmt.Errorf("noise: unsupported suite %q", k.Suite)
	}
	dhBytes, err := base64.StdEncoding.DecodeString(k.X25519PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("noise: invalid X25519 base64: %w", err)
	}
	if len(dhBytes) != X25519PubLen {
		return nil, nil, errors.New("noise: invalid X25519 public key length")
	}
	dhPub, err = ecdh.X25519().NewPublicKey(dhBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("noise: invalid X25519 public key: %w", err)
	}
	kemBytes, err := base64.StdEncoding.DecodeString(k.MLKEM768PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("noise: invalid ML-KEM-768 base64: %w", err)
	}
	if len(kemBytes) != MLKEM768PubLen {
		return nil, nil, errors.New("noise: invalid ML-KEM-768 public key length")
	}
	kemPub, err = mlkem.NewEncapsulationKey768(kemBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("noise: invalid ML-KEM-768 public key: %w", err)
	}
	return dhPub, kemPub, nil
}
