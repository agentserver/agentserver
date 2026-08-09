package egressgateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
)

const LarkOpenAPIHost = larkegresspolicy.OpenAPIHost

type Config struct {
	Placeholders *egresscapability.Verifier
	ZTI          ZTIVerifier
	Authority    LiveAuthority
	Audit        AuditSink
	AllowedPSM   string
	Now          func() time.Time
}

type Service struct {
	placeholders *egresscapability.Verifier
	zti          ZTIVerifier
	authority    LiveAuthority
	audit        AuditSink
	allowedPSM   string
	now          func() time.Time
}

func NewService(config Config) (*Service, error) {
	if config.Placeholders == nil || config.ZTI == nil || config.Authority == nil || config.Audit == nil || config.Now == nil {
		return nil, errors.New("egress placeholder verifier, ZTI verifier, live authority, audit sink, and clock are required")
	}
	if !validBoundedText(config.AllowedPSM, 256) {
		return nil, errors.New("allowed TAE PSM is invalid")
	}
	return &Service{
		placeholders: config.Placeholders, zti: config.ZTI, authority: config.Authority,
		audit: config.Audit, allowedPSM: config.AllowedPSM, now: config.Now,
	}, nil
}

func (service *Service) Authorize(ctx context.Context, request OriginalRequest, outerZTIToken string) Decision {
	if service == nil || service.placeholders == nil || service.zti == nil || service.authority == nil || service.audit == nil || service.now == nil || ctx == nil {
		return Decision{ReasonCode: "service_unavailable"}
	}
	now := service.now().UTC()
	baseAudit := AuditRecord{At: now, Host: request.Host, Path: request.Path, Method: request.Method, Decision: "deny"}
	if err := validateOriginalRequest(request); err != nil {
		return service.deny(ctx, baseAudit, "invalid_request")
	}
	bodyZTI, ok := exactHeader(request.Headers, ZTIHeader)
	if !ok || !validBoundedText(outerZTIToken, 32*1024) || !equalSecret(outerZTIToken, bodyZTI) {
		return service.deny(ctx, baseAudit, "zti_mismatch")
	}
	principal, err := service.zti.VerifyZTI(ctx, outerZTIToken)
	if err != nil || principal.PSM != service.allowedPSM || !validBoundedText(principal.PSM, 256) {
		return service.deny(ctx, baseAudit, "zti_denied")
	}
	baseAudit.PSM = principal.PSM
	authorization, ok := exactHeader(request.Headers, AuthorizationHeader)
	if !ok || !strings.HasPrefix(authorization, "Bearer ") || strings.Count(authorization, " ") != 1 {
		return service.deny(ctx, baseAudit, "placeholder_missing")
	}
	placeholder := strings.TrimPrefix(authorization, "Bearer ")
	claims, err := service.placeholders.Verify(placeholder, now)
	if err != nil {
		return service.deny(ctx, baseAudit, "placeholder_invalid")
	}
	baseAudit = bindClaimsAudit(baseAudit, claims)
	if !allowLarkReadOnly(request.Host, request.Path, request.Method) {
		return service.deny(ctx, baseAudit, "lark_policy_denied")
	}
	if claims.PolicySHA256 != larkegresspolicy.SHA256Hex() {
		return service.deny(ctx, baseAudit, "lark_policy_version_denied")
	}
	credential, err := service.authority.AuthorizeLarkReadOnly(ctx, claims, principal)
	if err != nil || !validAccessToken(credential.AccessToken) || !credential.ExpiresAt.After(now.Add(time.Second)) {
		return service.deny(ctx, baseAudit, "live_authority_denied")
	}
	allowAudit := baseAudit
	allowAudit.Decision = "allow"
	allowAudit.ReasonCode = "allowed"
	if err := service.audit.RecordEgressDecision(ctx, allowAudit); err != nil {
		return Decision{ReasonCode: "audit_unavailable"}
	}
	return Decision{
		Allow: true, ReasonCode: "allowed",
		Headers: map[string]string{AuthorizationHeader: "Bearer " + credential.AccessToken},
	}
}

func (service *Service) deny(ctx context.Context, record AuditRecord, reason string) Decision {
	record.Decision = "deny"
	record.ReasonCode = reason
	if err := service.audit.RecordEgressDecision(ctx, record); err != nil {
		reason = "audit_unavailable"
	}
	return Decision{ReasonCode: reason}
}

func bindClaimsAudit(record AuditRecord, claims egresscapability.Claims) AuditRecord {
	record.CapabilityID = claims.CapabilityID
	record.WorkspaceID = claims.WorkspaceID
	record.SessionID = claims.SessionID
	record.ActorID = claims.ActorID
	record.EnvironmentID = claims.EnvironmentID
	record.RunID = claims.RunID
	record.RunAttemptID = claims.RunAttemptID
	record.RunAttemptGeneration = claims.RunAttemptGeneration
	record.ExecutionID = claims.ExecutionID
	record.OperationID = claims.OperationID
	record.SandboxID = claims.SandboxID
	record.TargetGeneration = claims.TargetGeneration
	record.GrantID = claims.GrantID
	record.GrantVersion = claims.GrantVersion
	return record
}

func validateOriginalRequest(request OriginalRequest) error {
	if request.Host != LarkOpenAPIHost || strings.ToLower(request.Host) != request.Host || net.ParseIP(request.Host) != nil {
		return errors.New("host is not the exact Lark OpenAPI host")
	}
	if !validBoundedText(request.Method, 16) || strings.ToUpper(request.Method) != request.Method {
		return errors.New("method is not canonical uppercase")
	}
	if !validBoundedText(request.Path, 4096) || !strings.HasPrefix(request.Path, "/") ||
		strings.ContainsAny(request.Path, "\\%?#\x00") || strings.Contains(request.Path, "//") ||
		path.Clean(request.Path) != request.Path || (len(request.Path) > 1 && strings.HasSuffix(request.Path, "/")) {
		return errors.New("path is not canonical")
	}
	if len(request.Headers) < 1 || len(request.Headers) > 128 {
		return errors.New("headers are missing or excessive")
	}
	names := make(map[string]struct{}, len(request.Headers))
	for name, value := range request.Headers {
		if !validHTTPHeaderName(name) || !validBoundedText(value, 32*1024) {
			return errors.New("header is invalid")
		}
		canonicalName := strings.ToLower(name)
		if _, duplicate := names[canonicalName]; duplicate || forbiddenOriginalHeader(canonicalName) {
			return errors.New("header is ambiguous or unsafe for the read-only policy")
		}
		names[canonicalName] = struct{}{}
	}
	return nil
}

func forbiddenOriginalHeader(name string) bool {
	switch name {
	case "connection", "content-length", "cookie", "forwarded", "host", "keep-alive",
		"proxy-authenticate", "proxy-authorization", "proxy-connection", "set-cookie", "te", "trailer",
		"transfer-encoding", "upgrade", "x-http-method-override", "x-method-override", "x-original-method":
		return true
	default:
		return strings.HasPrefix(name, "x-forwarded-")
	}
}

func validHTTPHeaderName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') {
			continue
		}
		switch character {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func allowLarkReadOnly(host, requestPath, method string) bool {
	return larkegresspolicy.Allows(host, requestPath, method)
}

func exactHeader(headers map[string]string, wanted string) (string, bool) {
	var value string
	found := false
	for name, candidate := range headers {
		if !strings.EqualFold(name, wanted) {
			continue
		}
		if found || candidate == "" {
			return "", false
		}
		found = true
		value = candidate
	}
	return value, found
}

func validAccessToken(value string) bool {
	return validBoundedText(value, 16*1024) && !strings.ContainsAny(value, " \t\r\n")
}

func equalSecret(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func validBoundedText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
