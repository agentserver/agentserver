package executorgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	AgentxEnrollmentPath = "/internal/v2/agentx/enrollments"
	AgentxChallengePath  = "/internal/v2/agentx/challenges"

	AgentxChallengeIDHeader  = "X-Agentserver-Challenge-Id"
	AgentxMachineProofHeader = "X-Agentserver-Machine-Proof"

	ExecutorWSSProofVersion = "executor-wss-proof/ed25519-v1"

	MaximumExecutorChallengeTTL = 30 * time.Second

	maximumExecutorIdentityBodyBytes = int64(512 * 1024)
	maximumExecutorBearerBytes       = 8192
	maximumExecutorChallenges        = 4096
)

var executorWSSProofDomain = []byte("agentserver-v2/executor-wss-proof/ed25519-v1\x00")

var errExecutorAuthenticationUnavailable = errors.New("executor authentication authority is unavailable")

// ExecutorMachineAuthority is the validated non-secret authority returned by
// Core after live Hydra introspection and exact client reconciliation.
type ExecutorMachineAuthority struct {
	ExecutorID              string
	WorkspaceID             string
	OAuthClientID           string
	MachinePublicKeyEd25519 ed25519.PublicKey
	MachineKeySHA256        [sha256.Size]byte
	ExecutorVersion         int64
	TokenExpiresAt          time.Time
	AuthorizedAt            time.Time
}

type ExecutorIdentityCore interface {
	CompleteExecutorEnrollment(context.Context, string, string, corecontract.CompleteExecutorEnrollmentRequest) (corecontract.CompleteExecutorEnrollmentResponse, error)
	AuthorizeExecutorConnection(context.Context, string) (ExecutorMachineAuthority, error)
}

type ExecutorChallengeResponse struct {
	Version           string    `json:"version"`
	ChallengeID       string    `json:"challengeId"`
	Challenge         string    `json:"challenge"`
	ExecutorID        string    `json:"executorId"`
	MachineKeySHA256  string    `json:"machineKeySha256"`
	GatewayInstanceID string    `json:"gatewayInstanceId"`
	Target            string    `json:"target"`
	IssuedAt          time.Time `json:"issuedAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type ExecutorChallengeConfig struct {
	GatewayInstanceID  string
	ExpectedExecutorID string
	Target             string
	TTL                time.Duration
	MaximumOutstanding int
	IDGenerator        IDGenerator
	Entropy            io.Reader
	Now                func() time.Time
}

func DefaultExecutorChallengeConfig(gatewayInstanceID, expectedExecutorID string) ExecutorChallengeConfig {
	return ExecutorChallengeConfig{
		GatewayInstanceID:  gatewayInstanceID,
		ExpectedExecutorID: expectedExecutorID,
		Target:             AgentxConnectPath,
		TTL:                MaximumExecutorChallengeTTL,
		MaximumOutstanding: maximumExecutorChallenges,
		IDGenerator:        newRandomUUID,
		Entropy:            rand.Reader,
		Now:                time.Now,
	}
}

type executorChallenge struct {
	response         ExecutorChallengeResponse
	nonce            [32]byte
	bearerSHA256     [sha256.Size]byte
	workspaceID      string
	oauthClientID    string
	machinePublicKey ed25519.PublicKey
	machineKeySHA256 [sha256.Size]byte
	executorVersion  int64
	tokenExpiresAt   time.Time
	authorizedAt     time.Time
}

type ExecutorChallengeAuthority struct {
	core   ExecutorIdentityCore
	config ExecutorChallengeConfig

	mu         sync.Mutex
	challenges map[string]executorChallenge
}

func NewExecutorChallengeAuthority(core ExecutorIdentityCore, config ExecutorChallengeConfig) (*ExecutorChallengeAuthority, error) {
	if core == nil {
		return nil, errors.New("executor identity Core client is required")
	}
	if err := validateRegistryIdentity("gateway instance ID", config.GatewayInstanceID); err != nil {
		return nil, err
	}
	if err := validateRegistryIdentity("expected executor ID", config.ExpectedExecutorID); err != nil {
		return nil, err
	}
	if config.Target != AgentxConnectPath {
		return nil, errors.New("executor challenge target must be the exact agentx connect path")
	}
	if config.TTL <= 0 || config.TTL > MaximumExecutorChallengeTTL || config.TTL%time.Millisecond != 0 {
		return nil, errors.New("executor challenge TTL must be a positive whole-millisecond duration no greater than 30 seconds")
	}
	if config.MaximumOutstanding < 1 || config.MaximumOutstanding > maximumExecutorChallenges {
		return nil, fmt.Errorf("maximum outstanding executor challenges must be between 1 and %d", maximumExecutorChallenges)
	}
	if config.IDGenerator == nil || config.Entropy == nil || config.Now == nil {
		return nil, errors.New("executor challenge ID generator, entropy source, and clock are required")
	}
	return &ExecutorChallengeAuthority{
		core: core, config: config, challenges: make(map[string]executorChallenge),
	}, nil
}

func (authority *ExecutorChallengeAuthority) Issue(ctx context.Context, bearer string) (ExecutorChallengeResponse, error) {
	if authority == nil || authority.core == nil {
		return ExecutorChallengeResponse{}, errors.New("executor challenge authority is unavailable")
	}
	machine, err := authority.core.AuthorizeExecutorConnection(ctx, bearer)
	if err != nil {
		return ExecutorChallengeResponse{}, err
	}
	if err := validateMachineAuthority(machine, authority.config.ExpectedExecutorID); err != nil {
		return ExecutorChallengeResponse{}, err
	}
	now := authority.config.Now().UTC().Truncate(time.Millisecond)
	expiresAt := now.Add(authority.config.TTL)
	tokenExpiresAt := machine.TokenExpiresAt.UTC().Truncate(time.Millisecond)
	if tokenExpiresAt.Before(expiresAt) {
		expiresAt = tokenExpiresAt
	}
	if !expiresAt.After(now) {
		return ExecutorChallengeResponse{}, errors.New("executor OAuth token expires before a challenge can be issued")
	}
	challengeID, err := authority.config.IDGenerator()
	if err != nil {
		return ExecutorChallengeResponse{}, fmt.Errorf("allocate executor challenge identity: %w", err)
	}
	if err := validateRegistryIdentity("executor challenge ID", challengeID); err != nil {
		return ExecutorChallengeResponse{}, err
	}
	var nonce [32]byte
	if _, err := io.ReadFull(authority.config.Entropy, nonce[:]); err != nil {
		return ExecutorChallengeResponse{}, fmt.Errorf("read executor challenge entropy: %w", err)
	}
	if subtle.ConstantTimeCompare(nonce[:], make([]byte, len(nonce))) == 1 {
		return ExecutorChallengeResponse{}, errors.New("executor challenge entropy returned an all-zero nonce")
	}
	bearerDigest := sha256.Sum256([]byte(bearer))
	response := ExecutorChallengeResponse{
		Version: ExecutorWSSProofVersion, ChallengeID: challengeID,
		Challenge:         base64.RawURLEncoding.EncodeToString(nonce[:]),
		ExecutorID:        machine.ExecutorID,
		MachineKeySHA256:  hex.EncodeToString(machine.MachineKeySHA256[:]),
		GatewayInstanceID: authority.config.GatewayInstanceID,
		Target:            authority.config.Target,
		IssuedAt:          now,
		ExpiresAt:         expiresAt,
	}
	record := executorChallenge{
		response: response, nonce: nonce, bearerSHA256: bearerDigest,
		workspaceID: machine.WorkspaceID, oauthClientID: machine.OAuthClientID,
		machinePublicKey: append(ed25519.PublicKey(nil), machine.MachinePublicKeyEd25519...),
		machineKeySHA256: machine.MachineKeySHA256, executorVersion: machine.ExecutorVersion,
		tokenExpiresAt: machine.TokenExpiresAt, authorizedAt: machine.AuthorizedAt,
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.pruneExpiredLocked(now)
	if len(authority.challenges) >= authority.config.MaximumOutstanding {
		return ExecutorChallengeResponse{}, errors.New("executor challenge capacity is exhausted")
	}
	if _, exists := authority.challenges[challengeID]; exists {
		return ExecutorChallengeResponse{}, errors.New("executor challenge identity collided")
	}
	authority.challenges[challengeID] = record
	return response, nil
}

func (authority *ExecutorChallengeAuthority) ConsumeAndVerify(
	request *http.Request,
	bearer, challengeID, encodedSignature string,
	machine ExecutorMachineAuthority,
) (ExecutorIdentity, error) {
	if authority == nil || authority.config.Now == nil {
		return ExecutorIdentity{}, errors.New("executor challenge authority is unavailable")
	}
	if request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.Path != authority.config.Target ||
		request.URL.RawPath != "" || request.URL.RawQuery != "" || request.URL.ForceQuery {
		return ExecutorIdentity{}, errors.New("executor WSS request target is invalid")
	}
	if err := validateRegistryIdentity("executor challenge ID", challengeID); err != nil {
		return ExecutorIdentity{}, err
	}
	signature, err := decodeCanonicalRawURL("executor machine proof", encodedSignature, ed25519.SignatureSize)
	if err != nil {
		return ExecutorIdentity{}, err
	}
	now := authority.config.Now().UTC()
	authority.mu.Lock()
	authority.pruneExpiredLocked(now)
	record, exists := authority.challenges[challengeID]
	if exists {
		delete(authority.challenges, challengeID)
	}
	authority.mu.Unlock()
	if !exists {
		return ExecutorIdentity{}, errors.New("executor challenge is missing, expired, or already consumed")
	}
	if !record.response.ExpiresAt.After(now) || record.response.Target != request.URL.RequestURI() {
		return ExecutorIdentity{}, errors.New("executor challenge is expired or bound to another request target")
	}
	if err := validateMachineAuthority(machine, authority.config.ExpectedExecutorID); err != nil {
		return ExecutorIdentity{}, err
	}
	bearerDigest := sha256.Sum256([]byte(bearer))
	if subtle.ConstantTimeCompare(record.bearerSHA256[:], bearerDigest[:]) != 1 ||
		record.response.GatewayInstanceID != authority.config.GatewayInstanceID ||
		record.response.ExecutorID != machine.ExecutorID || record.workspaceID != machine.WorkspaceID ||
		record.oauthClientID != machine.OAuthClientID || record.executorVersion != machine.ExecutorVersion ||
		!record.tokenExpiresAt.Equal(machine.TokenExpiresAt) || !record.authorizedAt.Equal(machine.AuthorizedAt) ||
		subtle.ConstantTimeCompare(record.machineKeySHA256[:], machine.MachineKeySHA256[:]) != 1 ||
		subtle.ConstantTimeCompare(record.machinePublicKey, machine.MachinePublicKeyEd25519) != 1 {
		return ExecutorIdentity{}, errors.New("executor challenge authority changed before WSS upgrade")
	}
	transcript, err := executorWSSProofTranscript(record.response, record.nonce, record.bearerSHA256, record.machineKeySHA256)
	if err != nil {
		return ExecutorIdentity{}, err
	}
	if !ed25519.Verify(record.machinePublicKey, transcript, signature) {
		return ExecutorIdentity{}, errors.New("executor machine proof is invalid")
	}
	return ExecutorIdentity{ExecutorID: record.response.ExecutorID}, nil
}

func (authority *ExecutorChallengeAuthority) Outstanding() int {
	if authority == nil {
		return 0
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.pruneExpiredLocked(authority.config.Now().UTC())
	return len(authority.challenges)
}

func (authority *ExecutorChallengeAuthority) pruneExpiredLocked(now time.Time) {
	for id, challenge := range authority.challenges {
		if !challenge.response.ExpiresAt.After(now) {
			delete(authority.challenges, id)
		}
	}
}

// ExecutorWSSProofTranscript returns the exact message agentx signs. Every
// field is fixed-order and uint32be length-delimited after a versioned domain.
func ExecutorWSSProofTranscript(response ExecutorChallengeResponse, bearer string) ([]byte, error) {
	nonce, err := decodeCanonicalRawURL("executor challenge", response.Challenge, 32)
	if err != nil {
		return nil, err
	}
	machineDigest, err := decodeCanonicalHex("executor machine key fingerprint", response.MachineKeySHA256, sha256.Size)
	if err != nil {
		return nil, err
	}
	bearerDigest := sha256.Sum256([]byte(bearer))
	var nonceArray [32]byte
	copy(nonceArray[:], nonce)
	var machineArray [sha256.Size]byte
	copy(machineArray[:], machineDigest)
	return executorWSSProofTranscript(response, nonceArray, bearerDigest, machineArray)
}

func executorWSSProofTranscript(
	response ExecutorChallengeResponse,
	nonce [32]byte,
	bearerDigest, machineDigest [sha256.Size]byte,
) ([]byte, error) {
	if response.Version != ExecutorWSSProofVersion || response.Target != AgentxConnectPath ||
		response.IssuedAt.IsZero() || response.ExpiresAt.IsZero() || !response.ExpiresAt.After(response.IssuedAt) {
		return nil, errors.New("executor challenge response is outside the WSS proof profile")
	}
	if response.IssuedAt.Nanosecond()%int(time.Millisecond) != 0 || response.ExpiresAt.Nanosecond()%int(time.Millisecond) != 0 {
		return nil, errors.New("executor challenge timestamps must have whole-millisecond precision")
	}
	for name, value := range map[string]string{
		"challenge ID": response.ChallengeID, "executor ID": response.ExecutorID,
		"gateway instance ID": response.GatewayInstanceID,
	} {
		if err := validateRegistryIdentity(name, value); err != nil {
			return nil, err
		}
	}
	fields := [][]byte{
		[]byte(response.Version), []byte(response.ChallengeID), nonce[:],
		[]byte(response.ExecutorID), machineDigest[:], bearerDigest[:],
		[]byte(response.GatewayInstanceID), []byte(response.Target),
		encodeInt64(response.IssuedAt.UnixMilli()), encodeInt64(response.ExpiresAt.UnixMilli()),
	}
	capacity := len(executorWSSProofDomain)
	for _, field := range fields {
		if len(field) > 1<<20 {
			return nil, errors.New("executor WSS proof field exceeds protocol bounds")
		}
		capacity += 4 + len(field)
	}
	transcript := make([]byte, 0, capacity)
	transcript = append(transcript, executorWSSProofDomain...)
	var length [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		transcript = append(transcript, length[:]...)
		transcript = append(transcript, field...)
	}
	return transcript, nil
}

func encodeInt64(value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	return encoded[:]
}

type ProductionExecutorAuthenticator struct {
	core       ExecutorIdentityCore
	challenges *ExecutorChallengeAuthority
}

func NewProductionExecutorAuthenticator(core ExecutorIdentityCore, challenges *ExecutorChallengeAuthority) (*ProductionExecutorAuthenticator, error) {
	if core == nil || challenges == nil {
		return nil, errors.New("production executor Core client and challenge authority are required")
	}
	return &ProductionExecutorAuthenticator{core: core, challenges: challenges}, nil
}

func (authenticator *ProductionExecutorAuthenticator) AuthenticateExecutor(request *http.Request) (ExecutorIdentity, error) {
	if request == nil || authenticator == nil || authenticator.core == nil || authenticator.challenges == nil {
		return ExecutorIdentity{}, errors.New("production executor authenticator is unavailable")
	}
	bearer, err := exactExecutorOAuthBearer(request.Header)
	if err != nil {
		return ExecutorIdentity{}, err
	}
	challengeID, err := exactSingleHeader(request.Header, AgentxChallengeIDHeader, 36)
	if err != nil {
		return ExecutorIdentity{}, err
	}
	signature, err := exactSingleHeader(request.Header, AgentxMachineProofHeader, 86)
	if err != nil {
		return ExecutorIdentity{}, err
	}
	// This second Core call is deliberately before challenge consumption. A
	// transient Core/Hydra failure must fail closed without turning a valid
	// challenge into an authorization result inferred from cached state.
	machine, err := authenticator.core.AuthorizeExecutorConnection(request.Context(), bearer)
	if err != nil {
		var commandError *CoreCommandError
		if errors.As(err, &commandError) &&
			(commandError.HTTPStatus == http.StatusUnauthorized || commandError.HTTPStatus == http.StatusForbidden || commandError.HTTPStatus == http.StatusNotFound) {
			return ExecutorIdentity{}, errors.New("executor OAuth authority rejected the connection")
		}
		return ExecutorIdentity{}, errExecutorAuthenticationUnavailable
	}
	return authenticator.challenges.ConsumeAndVerify(request, bearer, challengeID, signature, machine)
}

type ExecutorIdentityHandler struct {
	core               ExecutorIdentityCore
	challenges         *ExecutorChallengeAuthority
	expectedExecutorID string
}

func NewExecutorIdentityHandler(core ExecutorIdentityCore, challenges *ExecutorChallengeAuthority) (*ExecutorIdentityHandler, error) {
	if core == nil || challenges == nil {
		return nil, errors.New("executor identity Core client and challenge authority are required")
	}
	return &ExecutorIdentityHandler{
		core: core, challenges: challenges, expectedExecutorID: challenges.config.ExpectedExecutorID,
	}, nil
}

func (handler *ExecutorIdentityHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+AgentxEnrollmentPath, handler)
	mux.Handle("POST "+AgentxChallengePath, handler)
	return mux
}

func (handler *ExecutorIdentityHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setExecutorIdentityHeaders(response)
	if request == nil || request.Method != http.MethodPost || request.URL == nil || request.URL.RawPath != "" ||
		request.URL.RawQuery != "" || request.URL.ForceQuery {
		writeExecutorIdentityHTTPError(response, http.StatusNotFound, "not_found", "executor identity endpoint not found")
		return
	}
	switch request.URL.Path {
	case AgentxEnrollmentPath:
		handler.completeEnrollment(response, request)
	case AgentxChallengePath:
		handler.issueChallenge(response, request)
	default:
		writeExecutorIdentityHTTPError(response, http.StatusNotFound, "not_found", "executor identity endpoint not found")
	}
}

func (handler *ExecutorIdentityHandler) completeEnrollment(response http.ResponseWriter, request *http.Request) {
	bearer, err := exactExecutorEnrollmentBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="executor-enrollment"`)
		writeExecutorIdentityHTTPError(response, http.StatusUnauthorized, "unauthorized", "a valid executor enrollment bearer is required")
		return
	}
	command, err := decodeGatewayEnrollmentRequest(response, request)
	if err != nil {
		writeExecutorIdentityHTTPError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	result, err := handler.core.CompleteExecutorEnrollment(request.Context(), bearer, handler.expectedExecutorID, command)
	if err != nil {
		writeGatewayCoreIdentityError(response, request, err)
		return
	}
	if err := validateEnrollmentResponse(result, handler.expectedExecutorID); err != nil {
		writeExecutorIdentityHTTPError(response, http.StatusBadGateway, "backend_contract_error", "Core returned an invalid executor enrollment result")
		return
	}
	writeExecutorIdentityJSON(response, http.StatusOK, result)
}

func (handler *ExecutorIdentityHandler) issueChallenge(response http.ResponseWriter, request *http.Request) {
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeExecutorIdentityHTTPError(response, http.StatusBadRequest, "invalid_argument", "executor challenge request body must be empty")
		return
	}
	bearer, err := exactExecutorOAuthBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="executor-gateway"`)
		writeExecutorIdentityHTTPError(response, http.StatusUnauthorized, "unauthorized", "a valid executor OAuth bearer is required")
		return
	}
	result, err := handler.challenges.Issue(request.Context(), bearer)
	if err != nil {
		writeGatewayCoreIdentityError(response, request, err)
		return
	}
	writeExecutorIdentityJSON(response, http.StatusCreated, result)
}

func decodeGatewayEnrollmentRequest(response http.ResponseWriter, request *http.Request) (corecontract.CompleteExecutorEnrollmentRequest, error) {
	var command corecontract.CompleteExecutorEnrollmentRequest
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return command, errors.New("Content-Type must be application/json")
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumExecutorIdentityBodyBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return command, errors.New("executor enrollment request exceeds its size limit or cannot be read")
	}
	limits := braincatalog.DefaultLimits()
	value, canonical, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumExecutorIdentityBodyBytes), limits)
	if err != nil {
		return command, errors.New("executor enrollment request is not canonical closed-world JSON")
	}
	if _, ok := value.(map[string]any); !ok {
		return command, errors.New("executor enrollment request must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, errors.New("executor enrollment request is outside the supported contract")
	}
	return command, nil
}

func exactExecutorEnrollmentBearer(header http.Header) (string, error) {
	bearer, err := exactBearer(header)
	if err != nil || !strings.HasPrefix(bearer, "asv2enr1.") {
		return "", errors.New("executor enrollment bearer is invalid")
	}
	return bearer, nil
}

func exactExecutorOAuthBearer(header http.Header) (string, error) {
	bearer, err := exactBearer(header)
	if err != nil {
		return "", errors.New("executor OAuth bearer is invalid")
	}
	return bearer, nil
}

func exactBearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("exactly one bearer authorization is required")
	}
	bearer := strings.TrimPrefix(values[0], "Bearer ")
	if bearer == "" || len(bearer) > maximumExecutorBearerBytes || strings.TrimSpace(bearer) != bearer ||
		strings.ContainsAny(bearer, " \t\x00\r\n") {
		return "", errors.New("bearer authorization framing is invalid")
	}
	return bearer, nil
}

func exactSingleHeader(header http.Header, name string, exactLength int) (string, error) {
	values := header.Values(name)
	if len(values) != 1 || len(values[0]) != exactLength || strings.TrimSpace(values[0]) != values[0] ||
		strings.ContainsAny(values[0], " \t,\x00\r\n") {
		return "", fmt.Errorf("%s header is invalid", name)
	}
	return values[0], nil
}

func validateMachineAuthority(machine ExecutorMachineAuthority, expectedExecutorID string) error {
	if err := validateRegistryIdentity("authorized executor ID", machine.ExecutorID); err != nil {
		return err
	}
	if machine.ExecutorID != expectedExecutorID {
		return errors.New("authorized executor is outside this gateway deployment")
	}
	if err := validateRegistryIdentity("authorized workspace ID", machine.WorkspaceID); err != nil {
		return err
	}
	if machine.OAuthClientID != "agentserver-executor-"+machine.ExecutorID || len(machine.MachinePublicKeyEd25519) != ed25519.PublicKeySize ||
		subtle.ConstantTimeCompare(machine.MachineKeySHA256[:], sha256Digest(machine.MachinePublicKeyEd25519)) != 1 ||
		machine.ExecutorVersion < 1 || machine.ExecutorVersion >= 1<<53 || machine.TokenExpiresAt.IsZero() || machine.AuthorizedAt.IsZero() {
		return errors.New("Core returned inconsistent executor machine authority")
	}
	return nil
}

func validateEnrollmentResponse(result corecontract.CompleteExecutorEnrollmentResponse, expectedExecutorID string) error {
	resource := result.Executor
	if err := validateRegistryIdentity("enrolled executor ID", resource.ExecutorID); err != nil {
		return err
	}
	if err := validateRegistryIdentity("enrolled workspace ID", resource.WorkspaceID); err != nil {
		return err
	}
	if resource.ExecutorID != expectedExecutorID || resource.Status != "offline" || resource.Version < 1 || resource.Version >= 1<<53 || resource.CreatedAt.IsZero() ||
		resource.UpdatedAt.IsZero() || resource.UpdatedAt.Before(resource.CreatedAt) ||
		result.OAuthClientID != "agentserver-executor-"+resource.ExecutorID || result.Audience != "executor-gateway" || result.Scope != "executor:connect" {
		return errors.New("Core enrollment response is outside the production profile")
	}
	return nil
}

func sha256Digest(value []byte) []byte {
	digest := sha256.Sum256(value)
	return digest[:]
}

func decodeCanonicalRawURL(name, value string, size int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != size || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%s is not canonical base64url", name)
	}
	return decoded, nil
}

func decodeCanonicalHex(name, value string, size int) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != size || hex.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%s is not canonical lowercase hexadecimal", name)
	}
	return decoded, nil
}

func setExecutorIdentityHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeExecutorIdentityJSON(response http.ResponseWriter, status int, value any) {
	setExecutorIdentityHeaders(response)
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeExecutorIdentityHTTPError(response http.ResponseWriter, status int, code, message string) {
	writeExecutorIdentityJSON(response, status, corecontract.ErrorResponse{Code: code, Message: message})
}

func writeGatewayCoreIdentityError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var commandError *CoreCommandError
	if errors.As(err, &commandError) {
		switch commandError.HTTPStatus {
		case http.StatusBadRequest:
			writeExecutorIdentityHTTPError(response, http.StatusBadRequest, "invalid_argument", "executor identity authority rejected the request")
			return
		case http.StatusUnauthorized:
			writeExecutorIdentityHTTPError(response, http.StatusUnauthorized, "unauthorized", "executor identity authority rejected the request")
			return
		case http.StatusForbidden:
			writeExecutorIdentityHTTPError(response, http.StatusForbidden, "forbidden", "executor identity authority rejected the request")
			return
		case http.StatusNotFound:
			writeExecutorIdentityHTTPError(response, http.StatusNotFound, "not_found", "executor identity authority rejected the request")
			return
		case http.StatusConflict:
			writeExecutorIdentityHTTPError(response, http.StatusConflict, "conflict", "executor identity authority rejected the request")
			return
		case http.StatusServiceUnavailable:
			writeExecutorIdentityHTTPError(response, http.StatusServiceUnavailable, "unavailable", "executor identity authority is temporarily unavailable")
			return
		}
	}
	writeExecutorIdentityHTTPError(response, http.StatusServiceUnavailable, "unavailable", "executor identity authority is temporarily unavailable")
}
