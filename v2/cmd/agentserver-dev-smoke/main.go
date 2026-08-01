// agentserver-dev-smoke is a TLS 1.3 host-side probe for the explicitly
// insecure single-container development deployment. It is not a product API
// client and accepts no production credentials.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
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
	finalMessage     = "Agentserver v2 scripted development turn completed."
	referenceMarker  = `data-agentserver-reference-web="v2"`
)

type smokeOptions struct {
	origin     string
	caFile     string
	bearerFile string
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
	flags.StringVar(&options.bearerFile, "bearer-file", "", "development browser bearer file")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return 2
	}
	if parent == nil {
		fmt.Fprintln(stderr, "agentserver-dev-smoke: context is required")
		return 1
	}
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
	fmt.Fprintf(
		stdout,
		"agentserver-dev-smoke: reference web loaded; AG-UI request %s reached RUN_FINISHED and request %s reached explicit user_cancelled\n",
		requestID,
		cancelRequestID,
	)
	return 0
}

func executeSmoke(parent context.Context, options smokeOptions) (string, []byte, error) {
	origin, bearer, client, closeClient, err := newSmokeHTTPClient(options)
	if err != nil {
		return "", nil, err
	}
	defer closeClient()

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
	origin, bearer, client, closeClient, err := newSmokeHTTPClient(options)
	if err != nil {
		return "", nil, err
	}
	defer closeClient()
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
					if err := sendSmokeApproval(ctx, client, origin, bearer, *approval); err != nil {
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
					if err := sendSmokeApproval(ctx, client, origin, bearer, *approval); err != nil {
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
	Version    int64
	Digest     struct {
		Domain               string `json:"domain"`
		CanonicalizerVersion string `json:"canonicalizerVersion"`
		SHA256               string `json:"sha256"`
	}
}

func pendingSmokeApproval(data []byte) (*smokeApproval, error) {
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
	if value.Status != "pending" {
		return nil, nil
	}
	if !validSmokeUUID(value.ApprovalID) || !validSmokeUUID(value.Nonce) || value.Version < 1 ||
		value.ContextDigest.Domain != "approval-context" ||
		value.ContextDigest.CanonicalizerVersion != "rfc8785-v1" ||
		!validSmokeSHA256(value.ContextDigest.SHA256) {
		return nil, errors.New("canonical approval event contains invalid command authority")
	}
	approval := &smokeApproval{
		ApprovalID: value.ApprovalID, Nonce: value.Nonce, Version: value.Version,
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
) error {
	body, err := json.Marshal(map[string]any{
		"decision": "approve", "nonce": approval.Nonce,
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
	if result.ExecutionStatus != "pending_approval" || result.Approval.ApprovalID != approval.ApprovalID ||
		result.Approval.Nonce != approval.Nonce || result.Approval.Status != "approved" ||
		result.Approval.Decision != "approve" || result.Approval.Version <= approval.Version {
		return fmt.Errorf("approval decision did not produce canonical approved state: %+v", result)
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

func newSmokeHTTPClient(options smokeOptions) (*url.URL, string, *http.Client, func(), error) {
	origin, err := validateOrigin(options.origin)
	if err != nil {
		return nil, "", nil, nil, err
	}
	caPEM, err := readBoundedFile("development CA", options.caFile, maximumFileBytes)
	if err != nil {
		return nil, "", nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, "", nil, nil, errors.New("development CA file contains no certificates")
	}
	bearerRaw, err := readBoundedFile("browser bearer", options.bearerFile, 16*1024)
	if err != nil {
		return nil, "", nil, nil, err
	}
	bearer := strings.TrimSuffix(string(bearerRaw), "\n")
	clear(bearerRaw)
	if bearer == "" || strings.TrimSpace(bearer) != bearer || strings.ContainsAny(bearer, "\x00\r\n") {
		return nil, "", nil, nil, errors.New("browser bearer file does not contain one canonical token line")
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
	return origin, bearer, &http.Client{Transport: transport}, transport.CloseIdleConnections, nil
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
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
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
