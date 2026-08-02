// Package executorenrollment validates and fingerprints the machine-owned
// portion of an executor enrollment request.
package executorenrollment

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	machineProofDomain = "agentserver-v2/executor-enrollment-proof/ed25519-v2\x00"
	oauthProofDomain   = "agentserver-v2/executor-enrollment-proof/es256-v1\x00"
)

type ValidatedRequest struct {
	MachinePublicKeyEd25519  [ed25519.PublicKeySize]byte
	MachineKeySHA256         [sha256.Size]byte
	OAuthPublicKeyP256X      [32]byte
	OAuthPublicKeyP256Y      [32]byte
	OAuthKeySHA256           [sha256.Size]byte
	EnrollmentRequestSHA256  [sha256.Size]byte
	AgentxVersion            string
	RuntimeManifestSHA256    [sha256.Size]byte
	ExecProtocolSourceSHA256 [sha256.Size]byte
	Environments             []coredb.ExecutorEnrollmentEnvironment
	machineProof             [ed25519.SignatureSize]byte
	oauthProof               [64]byte
}

type canonicalEnvironment struct {
	EnvironmentID       string          `json:"environmentId"`
	RootDescriptor      json.RawMessage `json:"rootDescriptor"`
	OwnerPolicySHA256   string          `json:"ownerPolicySha256"`
	Platform            string          `json:"platform"`
	CodexRelease        string          `json:"codexRelease"`
	CodexCommit         string          `json:"codexCommit"`
	CodexSHA256         string          `json:"codexSha256"`
	OuterProfileVersion string          `json:"outerProfileVersion"`
	ProcessMethods      []string        `json:"processMethods"`
	InsecureDev         bool            `json:"insecureDev"`
}

type canonicalRequest struct {
	MachinePublicKeyEd25519  string                 `json:"machinePublicKeyEd25519"`
	OAuthPublicKeyP256X      string                 `json:"oauthPublicKeyP256X"`
	OAuthPublicKeyP256Y      string                 `json:"oauthPublicKeyP256Y"`
	AgentxVersion            string                 `json:"agentxVersion"`
	RuntimeManifestSHA256    string                 `json:"runtimeManifestSha256"`
	ExecProtocolSourceSHA256 string                 `json:"execProtocolSourceSha256"`
	Environments             []canonicalEnvironment `json:"environments"`
}

func Validate(request corecontract.CompleteExecutorEnrollmentRequest) (ValidatedRequest, error) {
	publicKey, err := decodeCanonicalBase64("machine public key", request.MachinePublicKeyEd25519, ed25519.PublicKeySize)
	if err != nil {
		return ValidatedRequest{}, err
	}
	proof, err := decodeCanonicalBase64("machine proof", request.MachineProofEd25519, ed25519.SignatureSize)
	if err != nil {
		return ValidatedRequest{}, err
	}
	oauthX, err := decodeCanonicalBase64("OAuth P-256 public key x", request.OAuthPublicKeyP256X, 32)
	if err != nil {
		return ValidatedRequest{}, err
	}
	oauthY, err := decodeCanonicalBase64("OAuth P-256 public key y", request.OAuthPublicKeyP256Y, 32)
	if err != nil {
		return ValidatedRequest{}, err
	}
	oauthProof, err := decodeCanonicalBase64("OAuth ES256 proof", request.OAuthProofES256, 64)
	if err != nil {
		return ValidatedRequest{}, err
	}
	if !elliptic.P256().IsOnCurve(new(big.Int).SetBytes(oauthX), new(big.Int).SetBytes(oauthY)) {
		return ValidatedRequest{}, errors.New("OAuth P-256 public key is not on the P-256 curve")
	}
	runtimeDigest, err := decodeDigest("runtime manifest", request.RuntimeManifestSHA256)
	if err != nil {
		return ValidatedRequest{}, err
	}
	protocolDigest, err := decodeDigest("exec protocol source", request.ExecProtocolSourceSHA256)
	if err != nil {
		return ValidatedRequest{}, err
	}
	if request.AgentxVersion == "" || len(request.AgentxVersion) > 256 || strings.TrimSpace(request.AgentxVersion) != request.AgentxVersion || strings.ContainsAny(request.AgentxVersion, "\x00\r\n") {
		return ValidatedRequest{}, errors.New("agentx version must be canonical bounded text")
	}
	if len(request.Environments) < 1 || len(request.Environments) > 256 {
		return ValidatedRequest{}, errors.New("enrollment must contain between 1 and 256 environments")
	}

	ordered := append([]corecontract.ExecutorEnrollmentEnvironment(nil), request.Environments...)
	slices.SortFunc(ordered, func(left, right corecontract.ExecutorEnrollmentEnvironment) int {
		return strings.Compare(left.ID, right.ID)
	})
	canonicalEnvironments := make([]canonicalEnvironment, len(ordered))
	storedEnvironments := make([]coredb.ExecutorEnrollmentEnvironment, len(ordered))
	for index, environment := range ordered {
		if index > 0 && environment.ID == ordered[index-1].ID {
			return ValidatedRequest{}, fmt.Errorf("environment %d duplicates environment ID", index)
		}
		ownerPolicyDigest, err := decodeDigest("owner policy", environment.OwnerPolicySHA256)
		if err != nil {
			return ValidatedRequest{}, fmt.Errorf("environment %d: %w", index, err)
		}
		codexDigest, err := decodeDigest("Codex executable", environment.CodexSHA256)
		if err != nil {
			return ValidatedRequest{}, fmt.Errorf("environment %d: %w", index, err)
		}
		if environment.InsecureDev {
			return ValidatedRequest{}, fmt.Errorf("environment %d: production enrollment cannot set insecureDev", index)
		}
		if len(environment.RootDescriptor) < 2 || len(environment.RootDescriptor) > 64*1024 {
			return ValidatedRequest{}, fmt.Errorf("environment %d: root descriptor is empty or exceeds 64 KiB", index)
		}
		var rootDescriptor map[string]any
		if err := json.Unmarshal(environment.RootDescriptor, &rootDescriptor); err != nil || rootDescriptor == nil {
			return ValidatedRequest{}, fmt.Errorf("environment %d: root descriptor must be a JSON object", index)
		}
		if jsonValueContainsNUL(rootDescriptor) {
			return ValidatedRequest{}, fmt.Errorf("environment %d: root descriptor must not contain NUL", index)
		}
		canonicalEnvironments[index] = canonicalEnvironment{
			EnvironmentID: environment.ID, RootDescriptor: append(json.RawMessage(nil), environment.RootDescriptor...),
			OwnerPolicySHA256: environment.OwnerPolicySHA256, Platform: environment.Platform,
			CodexRelease: environment.CodexRelease, CodexCommit: environment.CodexCommit,
			CodexSHA256: environment.CodexSHA256, OuterProfileVersion: environment.OuterProfileVersion,
			ProcessMethods: append([]string(nil), environment.ProcessMethods...), InsecureDev: false,
		}
		storedEnvironments[index] = coredb.ExecutorEnrollmentEnvironment{
			ExecutorEnvironmentDeclaration: coredb.ExecutorEnvironmentDeclaration{
				ID: environment.ID, Platform: environment.Platform, CodexRelease: environment.CodexRelease,
				CodexCommit: environment.CodexCommit, CodexSHA256: codexDigest,
				OuterProfileVersion: environment.OuterProfileVersion,
				ProcessMethods:      append([]string(nil), environment.ProcessMethods...), InsecureDev: false,
			},
			RootDescriptor: append(json.RawMessage(nil), environment.RootDescriptor...), OwnerPolicySHA256: ownerPolicyDigest,
		}
	}

	raw, err := json.Marshal(canonicalRequest{
		MachinePublicKeyEd25519: request.MachinePublicKeyEd25519,
		OAuthPublicKeyP256X:     request.OAuthPublicKeyP256X, OAuthPublicKeyP256Y: request.OAuthPublicKeyP256Y,
		AgentxVersion:         request.AgentxVersion,
		RuntimeManifestSHA256: request.RuntimeManifestSHA256, ExecProtocolSourceSHA256: request.ExecProtocolSourceSHA256,
		Environments: canonicalEnvironments,
	})
	if err != nil {
		return ValidatedRequest{}, fmt.Errorf("encode executor enrollment request: %w", err)
	}
	_, digest, err := coredb.ValidateAndHashCanonicalJSON(coredb.HashDomainExecutorEnrollment, raw, func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || len(object) != 7 {
			return errors.New("executor enrollment fingerprint must be an exact object")
		}
		return nil
	})
	if err != nil {
		return ValidatedRequest{}, fmt.Errorf("fingerprint executor enrollment request: %w", err)
	}
	validated := ValidatedRequest{
		MachineKeySHA256: sha256.Sum256(publicKey), EnrollmentRequestSHA256: digest.SHA256(),
		OAuthKeySHA256: OAuthJWKThumbprint(request.OAuthPublicKeyP256X, request.OAuthPublicKeyP256Y),
		AgentxVersion:  request.AgentxVersion, RuntimeManifestSHA256: runtimeDigest,
		ExecProtocolSourceSHA256: protocolDigest, Environments: storedEnvironments,
	}
	copy(validated.MachinePublicKeyEd25519[:], publicKey)
	copy(validated.OAuthPublicKeyP256X[:], oauthX)
	copy(validated.OAuthPublicKeyP256Y[:], oauthY)
	copy(validated.machineProof[:], proof)
	copy(validated.oauthProof[:], oauthProof)
	return validated, nil
}

func jsonValueContainsNUL(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.ContainsRune(typed, 0)
	case []any:
		for _, item := range typed {
			if jsonValueContainsNUL(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if strings.ContainsRune(key, 0) || jsonValueContainsNUL(item) {
				return true
			}
		}
	}
	return false
}

func (request ValidatedRequest) VerifyProof(enrollmentToken string) error {
	if enrollmentToken == "" || len(enrollmentToken) > 8192 || strings.TrimSpace(enrollmentToken) != enrollmentToken {
		return errors.New("enrollment token is outside proof bounds")
	}
	message := ProofMessage(sha256.Sum256([]byte(enrollmentToken)), request.EnrollmentRequestSHA256)
	if !ed25519.Verify(ed25519.PublicKey(request.MachinePublicKeyEd25519[:]), message, request.machineProof[:]) {
		return errors.New("executor enrollment machine-key proof verification failed")
	}
	r := new(big.Int).SetBytes(request.oauthProof[:32])
	s := new(big.Int).SetBytes(request.oauthProof[32:])
	curve := elliptic.P256()
	if r.Sign() <= 0 || s.Sign() <= 0 || r.Cmp(curve.Params().N) >= 0 || s.Cmp(curve.Params().N) >= 0 ||
		s.Cmp(new(big.Int).Rsh(new(big.Int).Set(curve.Params().N), 1)) > 0 {
		return errors.New("executor enrollment OAuth proof is not canonical low-S ES256")
	}
	digest := OAuthProofDigest(sha256.Sum256([]byte(enrollmentToken)), request.EnrollmentRequestSHA256)
	publicKey := &ecdsa.PublicKey{
		Curve: curve, X: new(big.Int).SetBytes(request.OAuthPublicKeyP256X[:]), Y: new(big.Int).SetBytes(request.OAuthPublicKeyP256Y[:]),
	}
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return errors.New("executor enrollment OAuth-key proof verification failed")
	}
	return nil
}

func ProofMessage(tokenSHA256, requestSHA256 [sha256.Size]byte) []byte {
	message := make([]byte, 0, len(machineProofDomain)+2*sha256.Size)
	message = append(message, machineProofDomain...)
	message = append(message, tokenSHA256[:]...)
	message = append(message, requestSHA256[:]...)
	return message
}

func OAuthProofDigest(tokenSHA256, requestSHA256 [sha256.Size]byte) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(oauthProofDomain))
	_, _ = hash.Write(tokenSHA256[:])
	_, _ = hash.Write(requestSHA256[:])
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

// OAuthJWKThumbprint returns the RFC 7638 SHA-256 thumbprint of the exact
// public P-256 JWK used by Hydra. Validate must canonicalize x and y before
// calling it; keeping the encoded values here also avoids integer-width loss.
func OAuthJWKThumbprint(encodedX, encodedY string) [sha256.Size]byte {
	return sha256.Sum256([]byte(`{"crv":"P-256","kty":"EC","x":"` + encodedX + `","y":"` + encodedY + `"}`))
}

func decodeCanonicalBase64(field, encoded string, size int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != size || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("%s must be canonical unpadded base64url containing %d bytes", field, size)
	}
	allZero := true
	for _, value := range decoded {
		allZero = allZero && value == 0
	}
	if allZero {
		return nil, fmt.Errorf("%s must not be all zero", field)
	}
	return decoded, nil
}

func decodeDigest(field, encoded string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != len(digest) || hex.EncodeToString(decoded) != encoded {
		return digest, fmt.Errorf("%s digest must be lowercase 64-character SHA-256 hex", field)
	}
	copy(digest[:], decoded)
	if digest == [sha256.Size]byte{} {
		return digest, fmt.Errorf("%s digest must not be all zero", field)
	}
	return digest, nil
}
