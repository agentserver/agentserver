// agentserver-dev-smoke is a TLS 1.3 host-side probe for the explicitly
// insecure single-container development deployment. It is not a product API
// client and accepts no production credentials.
package main

import (
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
)

const (
	smokeWorkspaceID = "40000000-0000-4000-8000-000000000004"
	smokeSessionID   = "50000000-0000-4000-8000-000000000005"
	maximumSSEBytes  = 16 * 1024 * 1024
	maximumFileBytes = 1024 * 1024
	finalMessage     = "Agentserver v2 scripted development turn completed."
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
	fmt.Fprintf(stdout, "agentserver-dev-smoke: AG-UI request %s reached RUN_FINISHED with the scripted assistant message\n", requestID)
	return 0
}

func executeSmoke(parent context.Context, options smokeOptions) (string, []byte, error) {
	origin, err := validateOrigin(options.origin)
	if err != nil {
		return "", nil, err
	}
	caPEM, err := readBoundedFile("development CA", options.caFile, maximumFileBytes)
	if err != nil {
		return "", nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return "", nil, errors.New("development CA file contains no certificates")
	}
	bearerRaw, err := readBoundedFile("browser bearer", options.bearerFile, 16*1024)
	if err != nil {
		return "", nil, err
	}
	bearer := strings.TrimSuffix(string(bearerRaw), "\n")
	clear(bearerRaw)
	if bearer == "" || strings.TrimSpace(bearer) != bearer || strings.ContainsAny(bearer, "\x00\r\n") {
		return "", nil, errors.New("browser bearer file does not contain one canonical token line")
	}

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
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return requestID, nil, fmt.Errorf("send AG-UI request: %w", err)
	}
	defer response.Body.Close()
	stream, readErr := io.ReadAll(io.LimitReader(response.Body, maximumSSEBytes+1))
	if readErr != nil {
		return requestID, stream, fmt.Errorf("read AG-UI event stream: %w", readErr)
	}
	if len(stream) > maximumSSEBytes {
		return requestID, stream[:maximumSSEBytes], fmt.Errorf("AG-UI event stream exceeds %d bytes", maximumSSEBytes)
	}
	if response.StatusCode != http.StatusOK {
		return requestID, stream, fmt.Errorf("AG-UI status = %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return requestID, stream, fmt.Errorf("AG-UI Content-Type = %q", response.Header.Get("Content-Type"))
	}
	terminal, scripted, err := inspectSSE(stream)
	if err != nil {
		return requestID, stream, err
	}
	if !terminal {
		return requestID, stream, errors.New("AG-UI event stream has no RUN_FINISHED terminal")
	}
	if !scripted {
		return requestID, stream, errors.New("AG-UI event stream has no scripted final assistant message")
	}
	return requestID, stream, nil
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

func inspectSSE(stream []byte) (terminal, scripted bool, err error) {
	for _, line := range bytes.Split(stream, []byte{'\n'}) {
		data, found := bytes.CutPrefix(line, []byte("data: "))
		if !found {
			continue
		}
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return false, false, fmt.Errorf("decode AG-UI SSE data: %w", err)
		}
		if event.Type == "RUN_FINISHED" {
			terminal = true
		}
		if bytes.Contains(data, []byte(finalMessage)) {
			scripted = true
		}
	}
	return terminal, scripted, nil
}
