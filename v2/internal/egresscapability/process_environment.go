package egresscapability

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProcessEnvironmentVersion = 1

	processEnvironmentTokenPrefix     = "asv2proc1"
	processEnvironmentSignatureDomain = "agentserver-v2/process-environment-egress/ed25519-v1\x00"
	maximumProcessEnvironmentToken    = 1024
	processEnvironmentPayloadBytes    = 258
	processEnvironmentLarkProvider    = byte(1)
)

func IsProcessEnvironmentProof(token string) bool {
	return strings.HasPrefix(token, processEnvironmentTokenPrefix+".")
}

// ProcessEnvironmentClaims is the compact companion proof carried by the
// pinned lark-cli in X-Agent-Trace when a workspace selects process_env. The
// real provider token remains in Authorization; this proof gives the TAE
// Policy Webhook the exact Core operation tuple needed to reject unregistered
// bearer traffic and to recheck the workspace's live delivery mode.
type ProcessEnvironmentClaims struct {
	Version      int
	Issuer       string
	CapabilityID string

	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64

	ProviderKind      string
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	PolicySHA256      string
	IssuedAtUnixMS    int64
	ExpiresAtUnixMS   int64
}

func (claims ProcessEnvironmentClaims) Validate() error {
	if claims.Version != ProcessEnvironmentVersion || !validText(claims.Issuer, 512) || claims.ProviderKind != "lark" ||
		claims.RunAttemptGeneration < 1 || claims.TargetGeneration < 1 || claims.AuthorityVersion < 1 || claims.CredentialVersion < 1 ||
		!digestPattern.MatchString(claims.PolicySHA256) || claims.IssuedAtUnixMS < 1 || claims.ExpiresAtUnixMS <= claims.IssuedAtUnixMS ||
		claims.ExpiresAtUnixMS-claims.IssuedAtUnixMS > maximumLifetime.Milliseconds() {
		return errors.New("process environment egress proof authority is invalid")
	}
	for name, value := range map[string]string{
		"capabilityId": claims.CapabilityID, "workspaceId": claims.WorkspaceID,
		"sessionId": claims.SessionID, "actorId": claims.ActorID,
		"environmentId": claims.EnvironmentID, "runId": claims.RunID,
		"runAttemptId": claims.RunAttemptID, "executionId": claims.ExecutionID,
		"operationId": claims.OperationID, "sandboxId": claims.SandboxID,
		"bindingId": claims.BindingID,
	} {
		if _, err := decodeCanonicalUUID(value); err != nil {
			return fmt.Errorf("process environment egress proof %s is invalid", name)
		}
	}
	return nil
}

func (signer *Signer) SignProcessEnvironment(claims ProcessEnvironmentClaims) (string, error) {
	if signer == nil || len(signer.privateKey) != ed25519.PrivateKeySize || claims.Issuer != signer.issuer {
		return "", errors.New("process environment egress signer does not match the claims")
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	payload, err := encodeProcessEnvironmentClaims(claims)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(signer.privateKey, processEnvironmentSignatureMessage(signer.keyID, payload))
	token := strings.Join([]string{
		processEnvironmentTokenPrefix,
		base64.RawURLEncoding.EncodeToString([]byte(signer.keyID)),
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
	if len(token) > maximumProcessEnvironmentToken {
		return "", errors.New("process environment egress proof exceeds the lark-cli trace limit")
	}
	return token, nil
}

func (verifier *Verifier) VerifyProcessEnvironment(token string, now time.Time) (ProcessEnvironmentClaims, error) {
	if verifier == nil || token == "" || len(token) > maximumProcessEnvironmentToken || strings.TrimSpace(token) != token {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress verifier or proof is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != processEnvironmentTokenPrefix {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof framing is invalid")
	}
	keyID, err := decodePart(parts[1], 256)
	if err != nil || !validText(string(keyID), 256) {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof key ID is invalid")
	}
	key, found := verifier.keys[string(keyID)]
	if !found || key.Audience != AudienceForProvider("lark") {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof key is not trusted for Lark")
	}
	payload, err := decodePart(parts[2], processEnvironmentPayloadBytes)
	if err != nil || len(payload) != processEnvironmentPayloadBytes {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof payload is invalid")
	}
	signature, err := decodePart(parts[3], ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key.PublicKey, processEnvironmentSignatureMessage(key.KeyID, payload), signature) {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof signature is invalid")
	}
	claims, err := decodeProcessEnvironmentClaims(payload)
	if err != nil {
		return ProcessEnvironmentClaims{}, err
	}
	claims.Issuer = key.Issuer
	if err := claims.Validate(); err != nil {
		return ProcessEnvironmentClaims{}, err
	}
	if now.IsZero() || now.UnixMilli() < claims.IssuedAtUnixMS || now.UnixMilli() >= claims.ExpiresAtUnixMS {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof is not currently valid")
	}
	return claims, nil
}

func encodeProcessEnvironmentClaims(claims ProcessEnvironmentClaims) ([]byte, error) {
	payload := make([]byte, 0, processEnvironmentPayloadBytes)
	payload = append(payload, byte(ProcessEnvironmentVersion), processEnvironmentLarkProvider)
	for _, value := range []string{
		claims.CapabilityID, claims.WorkspaceID, claims.SessionID, claims.ActorID,
		claims.EnvironmentID, claims.RunID, claims.RunAttemptID, claims.ExecutionID,
		claims.OperationID, claims.SandboxID, claims.BindingID,
	} {
		decoded, err := decodeCanonicalUUID(value)
		if err != nil {
			return nil, err
		}
		payload = append(payload, decoded[:]...)
	}
	for _, value := range []int64{
		claims.RunAttemptGeneration, claims.TargetGeneration, claims.AuthorityVersion,
		claims.CredentialVersion, claims.IssuedAtUnixMS, claims.ExpiresAtUnixMS,
	} {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		payload = append(payload, encoded[:]...)
	}
	digest, err := hex.DecodeString(claims.PolicySHA256)
	if err != nil || len(digest) != 32 {
		return nil, errors.New("process environment policy digest is invalid")
	}
	payload = append(payload, digest...)
	if len(payload) != processEnvironmentPayloadBytes {
		return nil, errors.New("process environment egress proof payload size is invalid")
	}
	return payload, nil
}

func decodeProcessEnvironmentClaims(payload []byte) (ProcessEnvironmentClaims, error) {
	if len(payload) != processEnvironmentPayloadBytes || payload[0] != ProcessEnvironmentVersion || payload[1] != processEnvironmentLarkProvider {
		return ProcessEnvironmentClaims{}, errors.New("process environment egress proof version or provider is invalid")
	}
	offset := 2
	identities := make([]string, 11)
	for index := range identities {
		identities[index] = encodeCanonicalUUID(payload[offset : offset+16])
		offset += 16
	}
	values := make([]int64, 6)
	for index := range values {
		value := binary.BigEndian.Uint64(payload[offset : offset+8])
		if value > uint64(^uint64(0)>>1) {
			return ProcessEnvironmentClaims{}, errors.New("process environment egress proof integer is invalid")
		}
		values[index] = int64(value)
		offset += 8
	}
	return ProcessEnvironmentClaims{
		Version: ProcessEnvironmentVersion, ProviderKind: "lark",
		CapabilityID: identities[0], WorkspaceID: identities[1], SessionID: identities[2], ActorID: identities[3],
		EnvironmentID: identities[4], RunID: identities[5], RunAttemptID: identities[6], ExecutionID: identities[7],
		OperationID: identities[8], SandboxID: identities[9], BindingID: identities[10],
		RunAttemptGeneration: values[0], TargetGeneration: values[1], AuthorityVersion: values[2],
		CredentialVersion: values[3], IssuedAtUnixMS: values[4], ExpiresAtUnixMS: values[5],
		PolicySHA256: hex.EncodeToString(payload[offset : offset+32]),
	}, nil
}

func processEnvironmentSignatureMessage(keyID string, payload []byte) []byte {
	message := make([]byte, 0, len(processEnvironmentSignatureDomain)+2+len(keyID)+len(payload))
	message = append(message, processEnvironmentSignatureDomain...)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(keyID)))
	message = append(message, size[:]...)
	message = append(message, keyID...)
	return append(message, payload...)
}

func decodeCanonicalUUID(value string) ([16]byte, error) {
	var decoded [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return decoded, errors.New("UUID is not canonical")
	}
	compact := strings.ReplaceAll(value, "-", "")
	raw, err := hex.DecodeString(compact)
	if err != nil || len(raw) != len(decoded) || strings.ToLower(value) != value {
		return decoded, errors.New("UUID is not canonical")
	}
	copy(decoded[:], raw)
	var nonzero byte
	for _, value := range decoded {
		nonzero |= value
	}
	if nonzero == 0 {
		return [16]byte{}, errors.New("UUID is zero")
	}
	return decoded, nil
}

func encodeCanonicalUUID(raw []byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded)
}
