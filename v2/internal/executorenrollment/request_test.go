package executorenrollment

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestValidatedRequestCanonicalizesEnvironmentOrderAndVerifiesProof(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("e", ed25519.SeedSize)))
	oauthPrivateKey := testOAuthPrivateKey()
	request := enrollmentRequest(privateKey.Public().(ed25519.PublicKey), &oauthPrivateKey.PublicKey)
	request.Environments = append(request.Environments, request.Environments[0])
	request.Environments[0].ID = "60000000-0000-4000-8000-000000000007"
	request.Environments[0].RootDescriptor = json.RawMessage(`{"root":"/workspace/two","kind":"local"}`)
	request.Environments[1].ID = "60000000-0000-4000-8000-000000000006"
	request.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	request.OAuthProofES256 = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("o", 64)))
	validated, err := Validate(request)
	if err == nil {
		t.Fatal("zero proof must fail validation")
	}

	// First obtain the canonical digest with a non-zero placeholder proof, then
	// sign the exact versioned transcript and validate the final wire request.
	request.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", ed25519.SignatureSize)))
	validated, err = Validate(request)
	if err != nil {
		t.Fatal(err)
	}
	token := "asv2enr1.claims.mac"
	proof := ed25519.Sign(privateKey, ProofMessage(sha256.Sum256([]byte(token)), validated.EnrollmentRequestSHA256))
	request.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString(proof)
	request.OAuthProofES256 = base64.RawURLEncoding.EncodeToString(signOAuthProof(
		t, oauthPrivateKey, OAuthProofDigest(sha256.Sum256([]byte(token)), validated.EnrollmentRequestSHA256),
	))
	validated, err = Validate(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validated.VerifyProof(token); err != nil {
		t.Fatal(err)
	}
	if validated.Environments[0].ID != "60000000-0000-4000-8000-000000000006" ||
		validated.Environments[1].ID != "60000000-0000-4000-8000-000000000007" {
		t.Fatalf("canonical environment order = %+v", validated.Environments)
	}
	if err := validated.VerifyProof(token + "x"); err == nil {
		t.Fatal("proof was accepted for another enrollment bearer")
	}
	highSRequest := request
	highSProof, err := base64.RawURLEncoding.DecodeString(request.OAuthProofES256)
	if err != nil {
		t.Fatal(err)
	}
	highS := new(big.Int).Sub(elliptic.P256().Params().N, new(big.Int).SetBytes(highSProof[32:]))
	highS.FillBytes(highSProof[32:])
	highSRequest.OAuthProofES256 = base64.RawURLEncoding.EncodeToString(highSProof)
	highSValidated, err := Validate(highSRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := highSValidated.VerifyProof(token); err == nil || !strings.Contains(err.Error(), "low-S") {
		t.Fatalf("malleable high-S OAuth proof error = %v", err)
	}
}

func TestValidateRejectsDevelopmentDuplicateAndMalformedAuthority(t *testing.T) {
	publicKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("e", ed25519.SeedSize))).Public().(ed25519.PublicKey)
	base := enrollmentRequest(publicKey, &testOAuthPrivateKey().PublicKey)
	base.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", ed25519.SignatureSize)))
	base.OAuthProofES256 = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("o", 64)))
	tests := []struct {
		name   string
		mutate func(*corecontract.CompleteExecutorEnrollmentRequest)
	}{
		{name: "insecure environment", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) { value.Environments[0].InsecureDev = true }},
		{name: "duplicate environment", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.Environments = append(value.Environments, value.Environments[0])
		}},
		{name: "bad public key", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) { value.MachinePublicKeyEd25519 += "=" }},
		{name: "bad OAuth point", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.OAuthPublicKeyP256X = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
		}},
		{name: "zero digest", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.RuntimeManifestSHA256 = strings.Repeat("0", 64)
		}},
		{name: "bad root JSON", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.Environments[0].RootDescriptor = json.RawMessage(`[] trailing`)
		}},
		{name: "duplicate root field", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.Environments[0].RootDescriptor = json.RawMessage(`{"kind":"local","kind":"other","root":"/workspace"}`)
		}},
		{name: "NUL root field", mutate: func(value *corecontract.CompleteExecutorEnrollmentRequest) {
			value.Environments[0].RootDescriptor = json.RawMessage(`{"kind":"local","root":"/workspace\u0000suffix"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.Environments = append([]corecontract.ExecutorEnrollmentEnvironment(nil), base.Environments...)
			test.mutate(&request)
			if _, err := Validate(request); err == nil {
				t.Fatal("invalid enrollment request was accepted")
			}
		})
	}
}

func enrollmentRequest(publicKey ed25519.PublicKey, oauthPublicKey *ecdsa.PublicKey) corecontract.CompleteExecutorEnrollmentRequest {
	digest := sha256.Sum256([]byte("runtime"))
	protocol := sha256.Sum256([]byte("protocol"))
	codex := sha256.Sum256([]byte("codex"))
	policy := sha256.Sum256([]byte("policy"))
	return corecontract.CompleteExecutorEnrollmentRequest{
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(publicKey),
		OAuthPublicKeyP256X:     base64.RawURLEncoding.EncodeToString(oauthPublicKey.X.FillBytes(make([]byte, 32))),
		OAuthPublicKeyP256Y:     base64.RawURLEncoding.EncodeToString(oauthPublicKey.Y.FillBytes(make([]byte, 32))),
		AgentxVersion:           "0.1.0", RuntimeManifestSHA256: hex.EncodeToString(digest[:]),
		ExecProtocolSourceSHA256: hex.EncodeToString(protocol[:]),
		Environments: []corecontract.ExecutorEnrollmentEnvironment{{
			EnvironmentDeclaration: corecontract.EnvironmentDeclaration{
				ID: "60000000-0000-4000-8000-000000000006", Platform: "linux-amd64",
				CodexRelease: "0.146.0", CodexCommit: strings.Repeat("a", 40), CodexSHA256: hex.EncodeToString(codex[:]),
				OuterProfileVersion: "process-v1", ProcessMethods: []string{"process/start", "process/read", "process/write", "process/terminate"},
			},
			RootDescriptor: json.RawMessage(`{"kind":"local","root":"/workspace"}`), OwnerPolicySHA256: hex.EncodeToString(policy[:]),
		}},
	}
}

func testOAuthPrivateKey() *ecdsa.PrivateKey {
	curve := elliptic.P256()
	d := big.NewInt(42)
	x, y := curve.ScalarBaseMult(d.Bytes())
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
}

func signOAuthProof(t *testing.T, privateKey *ecdsa.PrivateKey, digest [sha256.Size]byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(privateKey.Curve.Params().N), 1)
	if s.Cmp(halfOrder) > 0 {
		s.Sub(privateKey.Curve.Params().N, s)
	}
	result := make([]byte, 64)
	r.FillBytes(result[:32])
	s.FillBytes(result[32:])
	return result
}
