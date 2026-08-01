// agentserver-dev-smoke is a TLS 1.3 host-side probe for the explicitly
// insecure single-container development deployment. It is not a product API
// client and accepts no production credentials.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devfixtures"
)

const (
	smokeWorkspaceID = "40000000-0000-4000-8000-000000000004"
	smokeSessionID   = "50000000-0000-4000-8000-000000000005"
	maximumSSEBytes  = 16 * 1024 * 1024
	maximumFileBytes = 1024 * 1024
	maximumWebBytes  = 1024 * 1024
	maximumAuthBytes = 64 * 1024
	finalMessage     = "Agentserver v2 scripted development turn completed."
	referenceMarker  = `data-agentserver-reference-web="v2"`

	smokeApprovalApprove = "approve"
	smokeApprovalDeny    = "deny"
)

type approvalGateSmokeMode string

const (
	approvalGateSmokeDeny          approvalGateSmokeMode = "deny"
	approvalGateSmokeExpiry        approvalGateSmokeMode = "expiry"
	approvalGateSmokePendingCancel approvalGateSmokeMode = "pending-cancel"
)

type approvalGateSmokeResult struct {
	RequestID  string
	RunID      string
	ApprovalID string
}

type smokeOptions struct {
	origin      string
	caFile      string
	accessToken string
	session     *smokeSession
}

type smokeSession struct {
	origin      *url.URL
	accessToken string
	client      *http.Client
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(parent context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agentserver-dev-smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options smokeOptions
	flags.StringVar(&options.origin, "origin", "", "published browser-gateway HTTPS origin")
	flags.StringVar(&options.caFile, "ca-file", "", "development CA PEM file")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return 2
	}
	if parent == nil {
		fmt.Fprintln(stderr, "agentserver-dev-smoke: context is required")
		return 1
	}
	session, closeSession, err := newSmokeSession(parent, options)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-dev-smoke: authenticate browser session: %v\n", err)
		return 1
	}
	defer closeSession()
	options.session = session
	requestID, eventStream, err := executeSmoke(parent, options)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-dev-smoke: %v\n", err)
		if len(eventStream) != 0 {
			_, _ = stderr.Write(eventStream)
			if eventStream[len(eventStream)-1] != '\n' {
				fmt.Fprintln(stderr)
			}
		}
		return 1
	}
	cancelRequestID, cancelStream, err := executeCancellationSmoke(parent, options)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-dev-smoke: cancellation smoke: %v\n", err)
		if len(cancelStream) != 0 {
			_, _ = stderr.Write(cancelStream)
			if cancelStream[len(cancelStream)-1] != '\n' {
				fmt.Fprintln(stderr)
			}
		}
		return 1
	}
	denied, deniedStream, err := executeApprovalGateSmoke(parent, options, approvalGateSmokeDeny)
	if err != nil {
		writeSmokeFailure(stderr, "denied approval smoke", err, deniedStream)
		return 1
	}
	expired, expiredStream, err := executeApprovalGateSmoke(parent, options, approvalGateSmokeExpiry)
	if err != nil {
		writeSmokeFailure(stderr, "expired approval smoke", err, expiredStream)
		return 1
	}
	pendingCancelled, pendingCancelledStream, err := executeApprovalGateSmoke(parent, options, approvalGateSmokePendingCancel)
	if err != nil {
		writeSmokeFailure(stderr, "pending approval cancellation smoke", err, pendingCancelledStream)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"agentserver-dev-smoke: OAuth Code + PKCE login and replay gates passed; reference web loaded; AG-UI request %s completed, request %s was cancelled after execution, and deny/expiry/pending-cancel gates contained all three pre-dispatch attempts\n",
		requestID,
		cancelRequestID,
	)
	fmt.Fprintf(stdout, "agentserver-dev-smoke-result denied %s %s\n", denied.RunID, denied.ApprovalID)
	fmt.Fprintf(stdout, "agentserver-dev-smoke-result expired %s %s\n", expired.RunID, expired.ApprovalID)
	fmt.Fprintf(stdout, "agentserver-dev-smoke-result pending-cancel %s %s\n", pendingCancelled.RunID, pendingCancelled.ApprovalID)
	return 0
}

func writeSmokeFailure(writer io.Writer, label string, err error, stream []byte) {
	fmt.Fprintf(writer, "agentserver-dev-smoke: %s: %v\n", label, err)
	if len(stream) == 0 {
		return
	}
	_, _ = writer.Write(stream)
	if stream[len(stream)-1] != '\n' {
		fmt.Fprintln(writer)
	}
}

func executeSmoke(parent context.Context, options smokeOptions) (string, []byte, error) {
	session, closeClient, err := newSmokeSession(parent, options)
	if err != nil {
		return "", nil, err
	}
	defer closeClient()
	origin, bearer, client := session.origin, session.accessToken, session.client

	requestID, err := newRequestID()
	if err != nil {
		return "", nil, err
	}
	body, err := json.Marshal(map[string]any{
		"threadId": smokeSessionID,
		"runId":    requestID,
		"messages": []map[string]string{{
			"id": "user-" + requestID, "role": "user",
			"content": "Run the deterministic Agentserver v2 development smoke.",
		}},
		"tools": []any{}, "context": []any{},
	})
	if err != nil {
		return requestID, nil, fmt.Errorf("encode AG-UI request: %w", err)
	}

	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	endpoint := origin.JoinPath("v2", "workspaces", smokeWorkspaceID, "sessions", smokeSessionID, "agui")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return requestID, nil, fmt.Errorf("create AG-UI request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("Content-Type", "application/json")

	if err := verifyReferenceWeb(ctx, client, origin); err != nil {
		return requestID, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return requestID, nil, fmt.Errorf("send AG-UI request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		stream, _ := io.ReadAll(io.LimitReader(response.Body, maximumSSEBytes+1))
		return requestID, stream, fmt.Errorf("AG-UI status = %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return requestID, nil, fmt.Errorf("AG-UI Content-Type = %q", response.Header.Get("Content-Type"))
	}
	stream, terminal, scripted, commandSurface, err := readCompletionSmokeStream(
		ctx, response.Body, client, origin, bearer,
	)
	if err != nil {
		return requestID, stream, err
	}
	if !terminal {
		return requestID, stream, errors.New("AG-UI event stream has no RUN_FINISHED terminal")
	}
	if !scripted {
		return requestID, stream, errors.New("AG-UI event stream has no scripted final assistant message")
	}
	if !commandSurface {
		return requestID, stream, errors.New("AG-UI event stream has no command A2UI surface")
	}
	return requestID, stream, nil
}

func executeCancellationSmoke(parent context.Context, options smokeOptions) (string, []byte, error) {
	session, closeClient, err := newSmokeSession(parent, options)
	if err != nil {
		return "", nil, err
	}
	defer closeClient()
	origin, bearer, client := session.origin, session.accessToken, session.client
	requestID, err := newRequestID()
	if err != nil {
		return "", nil, err
	}
	body, err := json.Marshal(map[string]any{
		"threadId": smokeSessionID,
		"runId":    requestID,
		"messages": []map[string]string{{
			"id": "user-" + requestID, "role": "user",
			"content": "Run the deterministic Agentserver v2 cancellation smoke. " + devfixtures.CancellationHoldMarker,
		}},
		"tools": []any{}, "context": []any{},
	})
	if err != nil {
		return requestID, nil, fmt.Errorf("encode cancellation AG-UI request: %w", err)
	}

	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	endpoint := origin.JoinPath("v2", "workspaces", smokeWorkspaceID, "sessions", smokeSessionID, "agui")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return requestID, nil, fmt.Errorf("create cancellation AG-UI request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return requestID, nil, fmt.Errorf("send cancellation AG-UI request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maximumSSEBytes+1))
		return requestID, raw, fmt.Errorf("cancellation AG-UI status = %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return requestID, nil, fmt.Errorf("cancellation AG-UI Content-Type = %q", response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReaderSize(io.LimitReader(response.Body, maximumSSEBytes+1), 128*1024)
	var stream bytes.Buffer
	serverRunID := ""
	cancelSent := false
	terminal := false
	decidedApprovals := make(map[string]int64)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) != 0 {
			_, _ = stream.Write(line)
			if stream.Len() > maximumSSEBytes {
				return requestID, append([]byte(nil), stream.Bytes()[:maximumSSEBytes]...), fmt.Errorf("cancellation AG-UI event stream exceeds %d bytes", maximumSSEBytes)
			}
			data, isData := bytes.CutPrefix(line, []byte("data: "))
			if isData {
				approval, err := pendingSmokeApproval(data)
				if err != nil {
					return requestID, append([]byte(nil), stream.Bytes()...), err
				}
				if approval != nil && decidedApprovals[approval.ApprovalID] < approval.Version {
					if err := sendSmokeApproval(ctx, client, origin, bearer, *approval, smokeApprovalApprove); err != nil {
						return requestID, append([]byte(nil), stream.Bytes()...), err
					}
					decidedApprovals[approval.ApprovalID] = approval.Version
				}
				var event struct {
					Type  string `json:"type"`
					RunID string `json:"runId"`
					Code  string `json:"code"`
				}
				if err := json.Unmarshal(data, &event); err != nil {
					return requestID, append([]byte(nil), stream.Bytes()...), fmt.Errorf("decode cancellation AG-UI SSE data: %w", err)
				}
				if event.Type == "RUN_STARTED" {
					if serverRunID != "" || event.RunID == "" || len(event.RunID) > 256 || strings.ContainsAny(event.RunID, "\x00\r\n/") {
						return requestID, append([]byte(nil), stream.Bytes()...), errors.New("cancellation AG-UI stream contains an invalid RUN_STARTED identity")
					}
					serverRunID = event.RunID
				}
				_, _, commandSurface, inspectErr := inspectSSE(line)
				if inspectErr != nil {
					return requestID, append([]byte(nil), stream.Bytes()...), inspectErr
				}
				if commandSurface && !cancelSent {
					if serverRunID == "" {
						return requestID, append([]byte(nil), stream.Bytes()...), errors.New("cancellation smoke reached its hold point before RUN_STARTED")
					}
					if err := sendSmokeCancellation(ctx, client, origin, bearer, serverRunID); err != nil {
						return requestID, append([]byte(nil), stream.Bytes()...), err
					}
					cancelSent = true
				}
				switch event.Type {
				case "RUN_FINISHED":
					return requestID, append([]byte(nil), stream.Bytes()...), errors.New("cancellation AG-UI request completed instead of being cancelled")
				case "RUN_ERROR":
					if event.Code != "user_cancelled" {
						return requestID, append([]byte(nil), stream.Bytes()...), fmt.Errorf("cancellation AG-UI terminal code = %q", event.Code)
					}
					terminal = true
				}
			}
		}
		if terminal {
			break
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return requestID, append([]byte(nil), stream.Bytes()...), fmt.Errorf("read cancellation AG-UI stream: %w", readErr)
			}
			break
		}
	}
	if !cancelSent {
		return requestID, append([]byte(nil), stream.Bytes()...), errors.New("cancellation AG-UI stream never reached the deterministic post-execution hold")
	}
	if !terminal {
		return requestID, append([]byte(nil), stream.Bytes()...), errors.New("cancellation AG-UI stream has no user_cancelled terminal")
	}
	return requestID, append([]byte(nil), stream.Bytes()...), nil
}

func executeApprovalGateSmoke(
	parent context.Context,
	options smokeOptions,
	mode approvalGateSmokeMode,
) (approvalGateSmokeResult, []byte, error) {
	marker, expectedApprovalStatus, err := approvalGateSmokeSpec(mode)
	if err != nil {
		return approvalGateSmokeResult{}, nil, err
	}
	session, closeClient, err := newSmokeSession(parent, options)
	if err != nil {
		return approvalGateSmokeResult{}, nil, err
	}
	defer closeClient()
	origin, bearer, client := session.origin, session.accessToken, session.client
	requestID, err := newRequestID()
	if err != nil {
		return approvalGateSmokeResult{}, nil, err
	}
	result := approvalGateSmokeResult{RequestID: requestID}
	body, err := json.Marshal(map[string]any{
		"threadId": smokeSessionID,
		"runId":    requestID,
		"messages": []map[string]string{{
			"id": "user-" + requestID, "role": "user",
			"content": "Run the deterministic Agentserver v2 " + string(mode) + " approval smoke. " + marker,
		}},
		"tools": []any{}, "context": []any{},
	})
	if err != nil {
		return result, nil, fmt.Errorf("encode %s AG-UI request: %w", mode, err)
	}

	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	endpoint := origin.JoinPath("v2", "workspaces", smokeWorkspaceID, "sessions", smokeSessionID, "agui")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return result, nil, fmt.Errorf("create %s AG-UI request: %w", mode, err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return result, nil, fmt.Errorf("send %s AG-UI request: %w", mode, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maximumSSEBytes+1))
		return result, raw, fmt.Errorf("%s AG-UI status = %d", mode, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return result, nil, fmt.Errorf("%s AG-UI Content-Type = %q", mode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReaderSize(io.LimitReader(response.Body, maximumSSEBytes+1), 128*1024)
	var stream bytes.Buffer
	var authority *smokeApproval
	lastApprovalVersion := int64(0)
	actionSent := false
	approvalSettled := false
	terminalType, terminalCode := "", ""
	failureMessage, commandSurface := false, false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) != 0 {
			_, _ = stream.Write(line)
			if stream.Len() > maximumSSEBytes {
				return result, append([]byte(nil), stream.Bytes()[:maximumSSEBytes]...), fmt.Errorf("%s AG-UI event stream exceeds %d bytes", mode, maximumSSEBytes)
			}
			data, isData := bytes.CutPrefix(line, []byte("data: "))
			if isData {
				var event struct {
					Type  string `json:"type"`
					RunID string `json:"runId"`
					Code  string `json:"code"`
				}
				if err := json.Unmarshal(data, &event); err != nil {
					return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("decode %s AG-UI SSE data: %w", mode, err)
				}
				if event.Type == "RUN_STARTED" {
					if result.RunID != "" || !validSmokeUUID(event.RunID) {
						return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s AG-UI stream contains an invalid RUN_STARTED identity", mode)
					}
					result.RunID = event.RunID
				}
				approval, err := smokeApprovalEvent(data)
				if err != nil {
					return result, append([]byte(nil), stream.Bytes()...), err
				}
				if approval != nil {
					if authority == nil {
						if approval.Status != "pending" {
							return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s approval stream began at status %q", mode, approval.Status)
						}
						copy := *approval
						authority = &copy
						result.ApprovalID = approval.ApprovalID
					} else if approval.ApprovalID != authority.ApprovalID || approval.Nonce != authority.Nonce || approval.Digest != authority.Digest {
						return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s approval event changed canonical authority", mode)
					}
					if approval.Version <= lastApprovalVersion {
						return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s approval version did not strictly increase", mode)
					}
					lastApprovalVersion = approval.Version
					if approval.Status == expectedApprovalStatus {
						approvalSettled = true
					} else if approval.Status != "pending" {
						return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s approval reached unexpected status %q", mode, approval.Status)
					}
					if approval.Status == "pending" && !actionSent {
						if result.RunID == "" {
							return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("%s approval arrived before RUN_STARTED", mode)
						}
						switch mode {
						case approvalGateSmokeDeny:
							err = sendSmokeApproval(ctx, client, origin, bearer, *approval, smokeApprovalDeny)
						case approvalGateSmokeExpiry:
							// Core database time and the signed per-attempt TTL settle this request.
						case approvalGateSmokePendingCancel:
							err = sendSmokeCancellation(ctx, client, origin, bearer, result.RunID)
						}
						if err != nil {
							return result, append([]byte(nil), stream.Bytes()...), err
						}
						actionSent = true
					}
				}
				failureMessage = failureMessage || bytes.Contains(data, []byte(devfixtures.ApprovalFailureMessage))
				_, _, lineCommandSurface, inspectErr := inspectSSE(line)
				if inspectErr != nil {
					return result, append([]byte(nil), stream.Bytes()...), inspectErr
				}
				commandSurface = commandSurface || lineCommandSurface
				switch event.Type {
				case "RUN_FINISHED", "RUN_ERROR":
					terminalType, terminalCode = event.Type, event.Code
				}
			}
		}
		if terminalType != "" {
			break
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, append([]byte(nil), stream.Bytes()...), fmt.Errorf("read %s AG-UI stream: %w", mode, readErr)
			}
			break
		}
	}
	streamBytes := append([]byte(nil), stream.Bytes()...)
	if result.RunID == "" || result.ApprovalID == "" || authority == nil || !actionSent {
		return result, streamBytes, fmt.Errorf("%s smoke did not observe complete pending approval authority", mode)
	}
	if !approvalSettled {
		return result, streamBytes, fmt.Errorf("%s smoke did not observe canonical approval status %q", mode, expectedApprovalStatus)
	}
	if commandSurface {
		return result, streamBytes, fmt.Errorf("%s approval gate emitted a command-result surface before dispatch authority", mode)
	}
	if mode == approvalGateSmokePendingCancel {
		if terminalType != "RUN_ERROR" || terminalCode != "user_cancelled" {
			return result, streamBytes, fmt.Errorf("pending-cancel terminal = %q/%q", terminalType, terminalCode)
		}
		return result, streamBytes, nil
	}
	if terminalType != "RUN_FINISHED" || !failureMessage {
		return result, streamBytes, fmt.Errorf("%s terminal/message = %q/%t", mode, terminalType, failureMessage)
	}
	return result, streamBytes, nil
}

func approvalGateSmokeSpec(mode approvalGateSmokeMode) (marker, expectedApprovalStatus string, err error) {
	switch mode {
	case approvalGateSmokeDeny:
		return devfixtures.ApprovalDenyMarker, "denied", nil
	case approvalGateSmokeExpiry:
		return devfixtures.ApprovalExpiryMarker, "expired", nil
	case approvalGateSmokePendingCancel:
		return devfixtures.ApprovalCancelMarker, "cancelled", nil
	default:
		return "", "", fmt.Errorf("unsupported approval gate smoke mode %q", mode)
	}
}

func readCompletionSmokeStream(
	ctx context.Context,
	body io.Reader,
	client *http.Client,
	origin *url.URL,
	bearer string,
) ([]byte, bool, bool, bool, error) {
	reader := bufio.NewReaderSize(io.LimitReader(body, maximumSSEBytes+1), 128*1024)
	var stream bytes.Buffer
	decidedApprovals := make(map[string]int64)
	terminal, scripted, commandSurface := false, false, false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) != 0 {
			_, _ = stream.Write(line)
			if stream.Len() > maximumSSEBytes {
				return append([]byte(nil), stream.Bytes()[:maximumSSEBytes]...), false, false, false,
					fmt.Errorf("AG-UI event stream exceeds %d bytes", maximumSSEBytes)
			}
			data, isData := bytes.CutPrefix(line, []byte("data: "))
			if isData {
				approval, err := pendingSmokeApproval(data)
				if err != nil {
					return append([]byte(nil), stream.Bytes()...), false, false, false, err
				}
				if approval != nil && decidedApprovals[approval.ApprovalID] < approval.Version {
					if err := sendSmokeApproval(ctx, client, origin, bearer, *approval, smokeApprovalApprove); err != nil {
						return append([]byte(nil), stream.Bytes()...), false, false, false, err
					}
					decidedApprovals[approval.ApprovalID] = approval.Version
				}
			}
			lineTerminal, lineScripted, lineCommand, err := inspectSSE(line)
			if err != nil {
				return append([]byte(nil), stream.Bytes()...), false, false, false, err
			}
			terminal = terminal || lineTerminal
			scripted = scripted || lineScripted
			commandSurface = commandSurface || lineCommand
		}
		if terminal {
			return append([]byte(nil), stream.Bytes()...), terminal, scripted, commandSurface, nil
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return append([]byte(nil), stream.Bytes()...), false, false, false,
					fmt.Errorf("read AG-UI event stream: %w", readErr)
			}
			return append([]byte(nil), stream.Bytes()...), terminal, scripted, commandSurface, nil
		}
	}
}

type smokeApproval struct {
	ApprovalID string
	Nonce      string
	Status     string
	Decision   string
	Version    int64
	Digest     struct {
		Domain               string `json:"domain"`
		CanonicalizerVersion string `json:"canonicalizerVersion"`
		SHA256               string `json:"sha256"`
	}
}

func pendingSmokeApproval(data []byte) (*smokeApproval, error) {
	approval, err := smokeApprovalEvent(data)
	if err != nil || approval == nil {
		return approval, err
	}
	if approval.Status != "pending" {
		return nil, nil
	}
	return approval, nil
}

func smokeApprovalEvent(data []byte) (*smokeApproval, error) {
	var event struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decode AG-UI SSE data: %w", err)
	}
	if event.Type != "CUSTOM" || event.Name != "agentserver.approval" {
		return nil, nil
	}
	var value struct {
		ApprovalID    string `json:"approvalId"`
		Nonce         string `json:"nonce"`
		Status        string `json:"status"`
		Decision      string `json:"decision"`
		Version       int64  `json:"version"`
		ContextDigest struct {
			Domain               string `json:"domain"`
			CanonicalizerVersion string `json:"canonicalizerVersion"`
			SHA256               string `json:"sha256"`
		} `json:"contextDigest"`
	}
	if err := json.Unmarshal(event.Value, &value); err != nil {
		return nil, fmt.Errorf("decode canonical approval authority: %w", err)
	}
	if !validSmokeUUID(value.ApprovalID) || !validSmokeUUID(value.Nonce) || value.Version < 1 ||
		value.ContextDigest.Domain != "approval-context" ||
		value.ContextDigest.CanonicalizerVersion != "rfc8785-v1" ||
		!validSmokeSHA256(value.ContextDigest.SHA256) {
		return nil, errors.New("canonical approval event contains invalid command authority")
	}
	switch value.Status {
	case "pending":
		if value.Decision != "" {
			return nil, errors.New("pending approval event contains terminal decision evidence")
		}
	case "approved", "consumed":
		if value.Decision != smokeApprovalApprove {
			return nil, errors.New("approved approval event lacks approve evidence")
		}
	case "denied":
		if value.Decision != smokeApprovalDeny {
			return nil, errors.New("denied approval event lacks deny evidence")
		}
	case "expired", "cancelled":
		if value.Decision != "" && value.Decision != smokeApprovalApprove {
			return nil, errors.New("terminal approval event contains invalid decision evidence")
		}
	default:
		return nil, fmt.Errorf("canonical approval event contains unsupported status %q", value.Status)
	}
	approval := &smokeApproval{
		ApprovalID: value.ApprovalID, Nonce: value.Nonce, Status: value.Status,
		Decision: value.Decision, Version: value.Version,
	}
	approval.Digest = value.ContextDigest
	return approval, nil
}

func sendSmokeApproval(
	ctx context.Context,
	client *http.Client,
	origin *url.URL,
	bearer string,
	approval smokeApproval,
	decision string,
) error {
	if decision != smokeApprovalApprove && decision != smokeApprovalDeny {
		return fmt.Errorf("unsupported smoke approval decision %q", decision)
	}
	body, err := json.Marshal(map[string]any{
		"decision": decision, "nonce": approval.Nonce,
		"contextDigest": approval.Digest, "expectedApprovalVersion": approval.Version,
	})
	if err != nil {
		return fmt.Errorf("encode approval decision: %w", err)
	}
	endpoint := origin.JoinPath(
		"v2", "workspaces", smokeWorkspaceID, "approvals", approval.ApprovalID+":decide",
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create approval decision request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send approval decision: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil {
		return fmt.Errorf("read approval decision response: %w", err)
	}
	if len(raw) > 64*1024 {
		return errors.New("approval decision response exceeds 64 KiB")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" {
		return fmt.Errorf(
			"approval decision response = status %d Content-Type %q body %q",
			response.StatusCode, response.Header.Get("Content-Type"), raw,
		)
	}
	var result struct {
		ExecutionStatus string `json:"executionStatus"`
		Approval        struct {
			ApprovalID string `json:"approvalId"`
			Nonce      string `json:"nonce"`
			Status     string `json:"status"`
			Decision   string `json:"decision"`
			Version    int64  `json:"version"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode approval decision response: %w", err)
	}
	wantExecutionStatus, wantApprovalStatus := "pending_approval", "approved"
	if decision == smokeApprovalDeny {
		wantExecutionStatus, wantApprovalStatus = "denied", "denied"
	}
	if result.ExecutionStatus != wantExecutionStatus || result.Approval.ApprovalID != approval.ApprovalID ||
		result.Approval.Nonce != approval.Nonce || result.Approval.Status != wantApprovalStatus ||
		result.Approval.Decision != decision || result.Approval.Version <= approval.Version {
		return fmt.Errorf("approval decision did not produce canonical %s state: %+v", wantApprovalStatus, result)
	}
	return nil
}

func validSmokeUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" ||
		value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && strings.ToLower(value) == value
}

func validSmokeSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value && value != strings.Repeat("0", 64)
}

func sendSmokeCancellation(ctx context.Context, client *http.Client, origin *url.URL, bearer, runID string) error {
	endpoint := origin.JoinPath("v2", "workspaces", smokeWorkspaceID, "runs", runID+":cancel")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create explicit cancel request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send explicit cancel request: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil {
		return fmt.Errorf("read explicit cancel response: %w", err)
	}
	if len(raw) > 64*1024 {
		return errors.New("explicit cancel response exceeds 64 KiB")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" {
		return fmt.Errorf("explicit cancel response = status %d Content-Type %q body %q", response.StatusCode, response.Header.Get("Content-Type"), raw)
	}
	var result struct {
		WorkspaceID string `json:"workspaceId"`
		SessionID   string `json:"sessionId"`
		RunID       string `json:"runId"`
		Status      string `json:"status"`
		RunVersion  int64  `json:"runVersion"`
		Terminal    bool   `json:"terminal"`
		Changed     bool   `json:"changed"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode explicit cancel response: %w", err)
	}
	if result.WorkspaceID != smokeWorkspaceID || result.SessionID != smokeSessionID || result.RunID != runID ||
		result.Status != "cancelling" || result.Terminal || !result.Changed || result.RunVersion < 1 {
		return fmt.Errorf("explicit cancel did not enter two-stage cancellation: %+v", result)
	}
	return nil
}

type smokeAuthorizationConfig struct {
	Version               int      `json:"version"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	RedirectPath          string   `json:"redirectPath"`
	ClientID              string   `json:"clientId"`
	Scopes                []string `json:"scopes"`
	Audience              string   `json:"audience"`
}

func newSmokeSession(parent context.Context, options smokeOptions) (*smokeSession, func(), error) {
	if parent == nil {
		return nil, nil, errors.New("smoke context is required")
	}
	if options.session != nil {
		if options.session.origin == nil || options.session.client == nil || !validSmokeAccessToken(options.session.accessToken) {
			return nil, nil, errors.New("preconfigured smoke session is invalid")
		}
		return options.session, func() {}, nil
	}
	origin, client, closeClient, err := newSmokeHTTPClient(options)
	if err != nil {
		return nil, nil, err
	}
	accessToken := options.accessToken
	if accessToken == "" {
		ctx, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		accessToken, err = authenticateSmoke(ctx, client, origin)
		if err != nil {
			closeClient()
			return nil, nil, err
		}
	}
	if !validSmokeAccessToken(accessToken) {
		closeClient()
		return nil, nil, errors.New("smoke access token is empty or outside protocol bounds")
	}
	return &smokeSession{origin: origin, accessToken: accessToken, client: client}, closeClient, nil
}

func newSmokeHTTPClient(options smokeOptions) (*url.URL, *http.Client, func(), error) {
	origin, err := validateOrigin(options.origin)
	if err != nil {
		return nil, nil, nil, err
	}
	caPEM, err := readBoundedFile("development CA", options.caFile, maximumFileBytes)
	if err != nil {
		return nil, nil, nil, err
	}
	defer clear(caPEM)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, nil, nil, errors.New("development CA file contains no certificates")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create smoke browser cookie jar: %w", err)
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second, KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    roots,
		},
	}
	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return origin, client, transport.CloseIdleConnections, nil
}

func authenticateSmoke(ctx context.Context, client *http.Client, origin *url.URL) (string, error) {
	if ctx == nil || client == nil || client.Jar == nil || origin == nil {
		return "", errors.New("OAuth smoke requires a browser client, cookie jar, and origin")
	}
	config, err := readSmokeAuthorizationConfig(ctx, client, origin)
	if err != nil {
		return "", err
	}
	verifier, err := newSmokePKCESecret()
	if err != nil {
		return "", err
	}
	state, err := newSmokePKCESecret()
	if err != nil {
		return "", err
	}
	nonce, err := newSmokePKCESecret()
	if err != nil {
		return "", err
	}
	challengeDigest := sha256.Sum256([]byte(verifier))
	redirectURI := origin.String() + config.RedirectPath
	authorizationURL := *origin
	authorizationURL.Path = config.AuthorizationEndpoint
	authorizationURL.RawQuery = (url.Values{
		"response_type":         {"code"},
		"client_id":             {config.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(config.Scopes, " ")},
		"audience":              {config.Audience},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challengeDigest[:])},
		"code_challenge_method": {"S256"},
	}).Encode()

	loginURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, &authorizationURL, "Hydra authorization")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(loginURL, "/auth/hydra/login"); err != nil {
		return "", err
	}
	idpURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, loginURL, "Hydra login bridge")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(idpURL, "/auth/idp/authorize"); err != nil {
		return "", err
	}
	externalCallbackURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, idpURL, "external OIDC authorization")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(externalCallbackURL, "/auth/oidc/callback"); err != nil {
		return "", err
	}
	binding, err := smokeLoginBinding(client.Jar, origin)
	if err != nil {
		return "", err
	}
	loginContinuationURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, externalCallbackURL, "external OIDC callback")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(loginContinuationURL, "/oauth2/auth"); err != nil {
		return "", err
	}
	if _, err := smokeLoginBinding(client.Jar, origin); err == nil {
		return "", errors.New("external OIDC callback did not clear its browser binding cookie")
	}
	consentURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, loginContinuationURL, "Hydra login continuation")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(consentURL, "/auth/hydra/consent"); err != nil {
		return "", err
	}
	consentContinuationURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, consentURL, "Hydra consent bridge")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(consentContinuationURL, "/oauth2/auth"); err != nil {
		return "", err
	}
	browserCallbackURL, err := followSmokeAuthorizationRedirect(ctx, client, origin, consentContinuationURL, "Hydra consent continuation")
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationPath(browserCallbackURL, "/"); err != nil {
		return "", err
	}
	browserCode, err := validateSmokeBrowserCallback(browserCallbackURL, state)
	if err != nil {
		return "", err
	}
	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {browserCode}, "redirect_uri": {redirectURI},
		"client_id": {config.ClientID}, "code_verifier": {verifier},
	}.Encode()
	accessToken, err := exchangeSmokeAuthorizationCode(ctx, client, origin, config, tokenForm)
	if err != nil {
		return "", err
	}
	if err := requireSmokeAuthorizationCodeReplayFailure(ctx, client, origin, config.TokenEndpoint, tokenForm); err != nil {
		return "", err
	}
	client.Jar.SetCookies(origin, []*http.Cookie{{
		Name: "__Host-agentserver-oidc", Value: binding, Path: "/", Secure: true, HttpOnly: true,
	}})
	if err := requireSmokeAuthorizationReplayFailure(ctx, client, origin, externalCallbackURL, "external OIDC callback", http.StatusBadRequest); err != nil {
		return "", err
	}
	client.Jar.SetCookies(origin, []*http.Cookie{{
		Name: "__Host-agentserver-oidc", Value: "", Path: "/", Secure: true, MaxAge: -1, Expires: time.Unix(1, 0),
	}})
	if err := requireSmokeAuthorizationReplayFailure(ctx, client, origin, consentURL, "Hydra consent", http.StatusServiceUnavailable); err != nil {
		return "", err
	}
	return accessToken, nil
}

func readSmokeAuthorizationConfig(ctx context.Context, client *http.Client, origin *url.URL) (smokeAuthorizationConfig, error) {
	endpoint := *origin
	endpoint.Path = "/auth/config"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return smokeAuthorizationConfig{}, fmt.Errorf("create browser authorization config request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return smokeAuthorizationConfig{}, fmt.Errorf("read browser authorization config: %w", err)
	}
	defer response.Body.Close()
	body, err := readSmokeAuthBody(response.Body, "browser authorization config")
	if err != nil {
		return smokeAuthorizationConfig{}, err
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		return smokeAuthorizationConfig{}, fmt.Errorf("browser authorization config response = status %d Content-Type %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var config smokeAuthorizationConfig
	if err := decodeSmokeJSON(body, &config); err != nil {
		return smokeAuthorizationConfig{}, fmt.Errorf("decode browser authorization config: %w", err)
	}
	if config.Version != 1 || config.AuthorizationEndpoint != "/oauth2/auth" || config.TokenEndpoint != "/oauth2/token" ||
		config.RedirectPath != "/" || config.ClientID != devfixtures.BrowserOAuthClientID ||
		config.Audience != devfixtures.BrowserTokenAudience ||
		!sameSmokeTextSet(config.Scopes, []string{"openid", devfixtures.BrowserTokenScope}) {
		return smokeAuthorizationConfig{}, errors.New("browser authorization config does not match the insecure development OAuth profile")
	}
	return config, nil
}

func followSmokeAuthorizationRedirect(
	ctx context.Context,
	client *http.Client,
	origin, endpoint *url.URL,
	label string,
) (*url.URL, error) {
	if !sameSmokeOrigin(origin, endpoint) || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("%s endpoint escaped the published browser origin", label)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", label, err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send %s request: %w", label, err)
	}
	defer response.Body.Close()
	body, readErr := readSmokeAuthBody(response.Body, label)
	if readErr != nil {
		return nil, readErr
	}
	locations := response.Header.Values("Location")
	if response.StatusCode != http.StatusFound || len(locations) != 1 || locations[0] == "" {
		return nil, fmt.Errorf("%s response = status %d Location %q body %q", label, response.StatusCode, response.Header.Get("Location"), body)
	}
	redirect, err := url.Parse(locations[0])
	if err != nil || !redirect.IsAbs() || !sameSmokeOrigin(origin, redirect) || redirect.User != nil || redirect.Fragment != "" || len(locations[0]) > 8192 {
		return nil, fmt.Errorf("%s returned an invalid or cross-origin redirect", label)
	}
	return redirect, nil
}

func exchangeSmokeAuthorizationCode(
	ctx context.Context,
	client *http.Client,
	origin *url.URL,
	config smokeAuthorizationConfig,
	form string,
) (string, error) {
	endpoint := *origin
	endpoint.Path = config.TokenEndpoint
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form))
	if err != nil {
		return "", fmt.Errorf("create browser token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("exchange browser authorization code: %w", err)
	}
	defer response.Body.Close()
	body, err := readSmokeAuthBody(response.Body, "browser token response")
	if err != nil {
		return "", err
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" {
		return "", fmt.Errorf("browser token response = status %d Content-Type %q body %q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := decodeSmokeJSON(body, &token); err != nil {
		return "", fmt.Errorf("decode browser token response: %w", err)
	}
	if !validSmokeAccessToken(token.AccessToken) || token.TokenType != "Bearer" || token.ExpiresIn < 1 || token.ExpiresIn > 24*60*60 ||
		!sameSmokeTextSet(strings.Fields(token.Scope), config.Scopes) {
		return "", errors.New("browser token response does not match requested OAuth authority")
	}
	return token.AccessToken, nil
}

func requireSmokeAuthorizationCodeReplayFailure(
	ctx context.Context,
	client *http.Client,
	origin *url.URL,
	tokenPath, form string,
) error {
	endpoint := *origin
	endpoint.Path = tokenPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form))
	if err != nil {
		return fmt.Errorf("create browser code replay request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send browser code replay request: %w", err)
	}
	defer response.Body.Close()
	body, err := readSmokeAuthBody(response.Body, "browser code replay response")
	if err != nil {
		return err
	}
	var oauthError struct {
		Error string `json:"error"`
	}
	if response.StatusCode != http.StatusBadRequest || decodeSmokeJSON(body, &oauthError) != nil || oauthError.Error != "invalid_grant" || response.Header.Get("Location") != "" {
		return fmt.Errorf("browser authorization code replay was not rejected canonically: status %d body %q", response.StatusCode, body)
	}
	return nil
}

func requireSmokeAuthorizationReplayFailure(
	ctx context.Context,
	client *http.Client,
	origin, endpoint *url.URL,
	label string,
	expectedStatus int,
) error {
	if !sameSmokeOrigin(origin, endpoint) {
		return fmt.Errorf("%s replay endpoint escaped the published browser origin", label)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create %s replay request: %w", label, err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send %s replay request: %w", label, err)
	}
	defer response.Body.Close()
	body, err := readSmokeAuthBody(response.Body, label+" replay response")
	if err != nil {
		return err
	}
	if response.StatusCode != expectedStatus || response.Header.Get("Location") != "" {
		return fmt.Errorf("%s replay was not rejected: status %d Location %q body %q", label, response.StatusCode, response.Header.Get("Location"), body)
	}
	return nil
}

func validateSmokeBrowserCallback(callback *url.URL, expectedState string) (string, error) {
	query := callback.Query()
	if len(query) != 2 || len(query["code"]) != 1 || len(query["state"]) != 1 ||
		query.Get("state") != expectedState || !validSmokeAccessToken(query.Get("code")) {
		return "", errors.New("Hydra browser callback did not contain one matching state and authorization code")
	}
	return query.Get("code"), nil
}

func smokeLoginBinding(jar http.CookieJar, origin *url.URL) (string, error) {
	value := ""
	for _, cookie := range jar.Cookies(origin) {
		if cookie.Name != "__Host-agentserver-oidc" {
			continue
		}
		if value != "" || !validSmokeAccessToken(cookie.Value) {
			return "", errors.New("OAuth browser binding cookie is duplicate or invalid")
		}
		value = cookie.Value
	}
	if value == "" {
		return "", errors.New("OAuth browser binding cookie is missing")
	}
	return value, nil
}

func requireSmokeAuthorizationPath(endpoint *url.URL, expected string) error {
	if endpoint == nil || endpoint.Path != expected || endpoint.RawPath != "" || endpoint.RawQuery == "" {
		return fmt.Errorf("OAuth redirect path = %v, want %s with a query", endpoint, expected)
	}
	return nil
}

func newSmokePKCESecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate OAuth PKCE correlation: %w", err)
	}
	defer clear(raw)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func readSmokeAuthBody(reader io.Reader, label string) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximumAuthBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if len(body) > maximumAuthBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maximumAuthBytes)
	}
	return body, nil
}

func decodeSmokeJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON response contains trailing data")
	}
	return nil
}

func sameSmokeOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && left.Scheme == "https" && right.Scheme == left.Scheme &&
		strings.EqualFold(right.Host, left.Host)
}

func sameSmokeTextSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value == "" {
			return false
		}
		values[value] = struct{}{}
	}
	if len(values) != len(left) {
		return false
	}
	for _, value := range right {
		if _, found := values[value]; !found {
			return false
		}
	}
	return true
}

func validSmokeAccessToken(value string) bool {
	if value == "" || len(value) > 8192 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._~+/=", character)) {
			return false
		}
	}
	return true
}

func verifyReferenceWeb(ctx context.Context, client *http.Client, origin *url.URL) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String()+"/", nil)
	if err != nil {
		return fmt.Errorf("create reference web request: %w", err)
	}
	request.Header.Set("Accept", "text/html")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("load reference web: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumWebBytes+1))
	if err != nil {
		return fmt.Errorf("read reference web: %w", err)
	}
	if len(body) > maximumWebBytes {
		return fmt.Errorf("reference web exceeds %d bytes", maximumWebBytes)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	policy := response.Header.Get("Content-Security-Policy")
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "text/html" ||
		!bytes.Contains(body, []byte(referenceMarker)) ||
		!strings.Contains(policy, "default-src 'self'") ||
		!strings.Contains(policy, "script-src 'self'") ||
		!strings.Contains(policy, "connect-src 'self'") ||
		response.Header.Get("Cache-Control") != "no-store" {
		return fmt.Errorf("reference web contract is invalid: status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	return nil
}

func validateOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.RawPath != "" || parsed.ForceQuery || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("origin must be an HTTPS origin without credentials, path, query, or fragment")
	}
	parsed.Path = ""
	return parsed, nil
}

func readBoundedFile(label, path string, maximum int64) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s file is required", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s file: %w", label, err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", label, err)
	}
	if len(contents) == 0 || int64(len(contents)) > maximum {
		return nil, fmt.Errorf("%s file must contain between 1 and %d bytes", label, maximum)
	}
	return contents, nil
}

func newRequestID() (string, error) {
	random := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return "", fmt.Errorf("generate smoke request ID: %w", err)
	}
	return "smoke-" + hex.EncodeToString(random), nil
}

func inspectSSE(stream []byte) (terminal, scripted, commandSurface bool, err error) {
	for _, line := range bytes.Split(stream, []byte{'\n'}) {
		data, found := bytes.CutPrefix(line, []byte("data: "))
		if !found {
			continue
		}
		var event struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return false, false, false, fmt.Errorf("decode AG-UI SSE data: %w", err)
		}
		if event.Type == "RUN_FINISHED" {
			terminal = true
		}
		if bytes.Contains(data, []byte(finalMessage)) {
			scripted = true
		}
		if event.Type == "CUSTOM" && event.Name == "a2ui.operations" {
			var operations []struct {
				CreateSurface *struct {
					SurfaceID string `json:"surfaceId"`
				} `json:"createSurface"`
				UpdateDataModel *struct {
					SurfaceID string `json:"surfaceId"`
					Value     struct {
						Command string `json:"command"`
						Output  string `json:"output"`
						Status  string `json:"status"`
					} `json:"value"`
				} `json:"updateDataModel"`
			}
			if err := json.Unmarshal(event.Value, &operations); err != nil {
				return false, false, false, fmt.Errorf("decode command A2UI operations: %w", err)
			}
			createdSurface := ""
			succeededSurface := ""
			for _, operation := range operations {
				if operation.CreateSurface != nil && strings.HasPrefix(operation.CreateSurface.SurfaceID, "command-") {
					createdSurface = operation.CreateSurface.SurfaceID
				}
				if operation.UpdateDataModel != nil && operation.UpdateDataModel.Value.Command == `["/bin/pwd"]` &&
					operation.UpdateDataModel.Value.Output == "/workspace\n" &&
					operation.UpdateDataModel.Value.Status == "succeeded (exit 0)" {
					succeededSurface = operation.UpdateDataModel.SurfaceID
				}
			}
			commandSurface = commandSurface || (createdSurface != "" && createdSurface == succeededSurface)
		}
	}
	return terminal, scripted, commandSurface, nil
}
