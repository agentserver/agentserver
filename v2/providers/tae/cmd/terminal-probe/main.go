package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

const (
	psm            = "bytedance.sandbox.agentserver"
	serviceAccount = "sa_g_eadce90936c71c1e"
	accessPath     = "/var/run/agentserver/material/bytecloud-access-key-id"
	secretPath     = "/var/run/agentserver/material/bytecloud-secret-access-key"
)

type report struct {
	SchemaVersion                         int       `json:"schemaVersion"`
	Kind                                  string    `json:"kind"`
	StartedAt                             time.Time `json:"startedAt"`
	FinishedAt                            time.Time `json:"finishedAt"`
	Passed                                bool      `json:"passed"`
	Region                                string    `json:"region"`
	PSM                                   string    `json:"psm"`
	ImageOmitted                          bool      `json:"imageOmitted"`
	SessionID                             string    `json:"sessionId,omitempty"`
	Created                               bool      `json:"created"`
	Ready                                 bool      `json:"ready"`
	SandboxdEnabled                       bool      `json:"sandboxdEnabled"`
	AuthorizationInspected                bool      `json:"authorizationInspected"`
	AuthorizedUsersConfigured             bool      `json:"authorizedUsersConfigured"`
	AuthorizedUsersContainsServiceAccount bool      `json:"authorizedUsersContainsServiceAccount"`
	ProcessSucceeded                      bool      `json:"processSucceeded"`
	ProcessOutput                         string    `json:"processOutput,omitempty"`
	ProviderCode                          string    `json:"providerCode,omitempty"`
	ProviderRequest                       string    `json:"providerRequestId,omitempty"`
	ProviderStatus                        int       `json:"providerStatus,omitempty"`
	Deleted                               bool      `json:"deleted"`
	Error                                 string    `json:"error,omitempty"`
}

func main() {
	result := run()
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if !result.Passed {
		os.Exit(1)
	}
}

func run() (result report) {
	result = report{
		SchemaVersion: 1,
		Kind:          "agentserver.tae.sg-terminal-probe",
		StartedAt:     time.Now().UTC(),
		Region:        "sg",
		PSM:           psm,
		ImageOmitted:  true,
	}
	defer func() { result.FinishedAt = time.Now().UTC() }()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	access, err := readCredential(accessPath)
	if err != nil {
		result.Error = "load ByteCloud application access key"
		return result
	}
	secret, err := readCredential(secretPath)
	if err != nil {
		result.Error = "load ByteCloud application secret key"
		return result
	}

	controlHTTP, err := adapter.NewSGTAEControlHTTPClient(adapter.StrictHTTPClientConfig{
		TotalTimeout: 60 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}, adapter.TAEProxyURLSG)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer controlHTTP.CloseIdleConnections()
	headerSource, err := adapter.NewByteCloudJWTHeaderSource(adapter.ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: access, SecretAccessKey: secret, Site: adapter.ByteCloudSiteI18NTT,
		JWTEndpoint: adapter.ByteCloudJWTEndpointSG, ProxyURL: adapter.TAEProxyURLSG,
		RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	refreshContext, cancelRefresh := context.WithTimeout(ctx, 10*time.Second)
	_, err = headerSource.ForceRefresh(refreshContext)
	cancelRefresh()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	control, err := adapter.NewSGSDKControlPlane(ctx, adapter.SDKControlPlaneConfig{
		PSM: psm, HTTPClient: controlHTTP, Headers: headerSource, RequestTimeout: 60 * time.Second,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	descriptorContext, cancelDescriptor := context.WithTimeout(ctx, 60*time.Second)
	descriptor, err := control.DescribeSandbox(descriptorContext)
	cancelDescriptor()
	if err != nil {
		result.Error = err.Error()
		return result
	}

	id, err := randomID()
	if err != nil {
		result.Error = "generate probe identity"
		return result
	}
	metadata := map[string]string{adapter.MetadataSandboxID: "agentserver-tae-terminal-probe-" + id}
	createContext, cancelCreate := context.WithTimeout(ctx, 60*time.Second)
	session, err := control.Create(createContext, adapter.CreateInput{
		TTL: 15 * time.Minute, Metadata: metadata,
	})
	cancelCreate()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.SessionID = session.ID
	result.Created = session.ID != ""
	defer func() {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancelCleanup()
		if session.ID == "" {
			return
		}
		deleteContext, cancelDelete := context.WithTimeout(cleanupContext, 60*time.Second)
		deleteErr := control.Delete(deleteContext, session.ID)
		cancelDelete()
		if deleteErr != nil && !errors.Is(deleteErr, adapter.ErrSessionNotFound) {
			if result.Error == "" {
				result.Error = deleteErr.Error()
			}
			return
		}
		result.Deleted = confirmDeleted(cleanupContext, control, session.ID)
		result.Passed = result.Created && result.Ready && result.ProcessSucceeded && result.Deleted && result.Error == ""
	}()
	result.AuthorizationInspected, result.AuthorizedUsersConfigured,
		result.AuthorizedUsersContainsServiceAccount = inspectSessionAuthorization(
		ctx, controlHTTP, headerSource, descriptor.ID, session.ID,
	)

	readyContext, cancelReady := context.WithTimeout(ctx, 5*time.Minute)
	ready, err := waitReady(readyContext, control, session.ID)
	cancelReady()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Ready = true
	result.SandboxdEnabled = ready.SandboxdEnabled

	dataHTTP, err := adapter.NewSGTAEDataHTTPClient(adapter.StrictHTTPClientConfig{
		ResponseHeaderTimeout: 30 * time.Second,
	}, adapter.TAEProxyURLSG)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer dataHTTP.CloseIdleConnections()
	suffix, err := adapter.SGDataplaneDomainSuffix()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	endpoint, err := adapter.NewSandboxdEndpointResolver(suffix)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	data, err := adapter.NewHTTPDataPlane(adapter.HTTPDataPlaneConfig{
		Client: dataHTTP, Headers: headerSource, Endpoint: endpoint,
		SandboxID: descriptor.ID, RequireHTTPS: true,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	processContext, cancelProcess := context.WithTimeout(ctx, 90*time.Second)
	output, err := runProcess(processContext, data, session.ID, id)
	cancelProcess()
	if err != nil {
		var requestError *adapter.RequestError
		if errors.As(err, &requestError) {
			result.ProviderCode = requestError.ProviderCode
			result.ProviderRequest = requestError.RequestID
			result.ProviderStatus = requestError.StatusCode
		}
		result.Error = err.Error()
		return result
	}
	result.ProcessSucceeded = true
	result.ProcessOutput = output
	return result
}

func inspectSessionAuthorization(
	ctx context.Context,
	client *http.Client,
	headers adapter.HeaderSource,
	sandboxID, sessionID string,
) (bool, bool, bool) {
	identityClient, err := adapter.NewIdentityHTTPClient(client, headers)
	if err != nil {
		return false, false, false
	}
	requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	endpoint := "https://" + adapter.SGTAEControlPlaneHost + "/api/v1/sandboxes/" +
		url.PathEscape(sandboxID) + "/sessions/" + url.PathEscape(sessionID)
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, false, false
	}
	response, err := identityClient.Do(request)
	if err != nil {
		return false, false, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, false, false
	}
	var envelope struct {
		Code int `json:"code"`
		Data *struct {
			Envs map[string]string `json:"envs"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	if err := decoder.Decode(&envelope); err != nil || envelope.Code != 0 || envelope.Data == nil {
		return false, false, false
	}
	authorizedUsers, configured := envelope.Data.Envs["AUTHORIZED_USERS"]
	return true, configured, configured && strings.Contains(authorizedUsers, serviceAccount)
}

func readCredential(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) == 0 || len(data) > 4096 {
		return "", errors.New("invalid credential material")
	}
	value := string(data)
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("invalid credential material")
	}
	return value, nil
}

func randomID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func waitReady(ctx context.Context, control adapter.ControlPlane, sessionID string) (adapter.ControlSession, error) {
	for {
		requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
		session, err := control.Get(requestContext, sessionID)
		cancel()
		if err != nil {
			return adapter.ControlSession{}, err
		}
		if session.Deleted {
			return adapter.ControlSession{}, errors.New("terminal session was deleted before becoming ready")
		}
		if session.SandboxdEnabled {
			return session, nil
		}
		select {
		case <-ctx.Done():
			return adapter.ControlSession{}, errors.New("terminal session readiness timed out")
		case <-time.After(time.Second):
		}
	}
}

func runProcess(ctx context.Context, data adapter.DataPlane, sessionID, id string) (string, error) {
	stream, err := data.StartProcess(ctx, sessionID, adapter.StartProcessInput{
		RequestID:  "agentserver-terminal-probe-" + id,
		Executable: "/bin/sh", Arguments: []string{"-lc", "printf terminal-ok"},
		WorkingDirectory: "/tmp", Environment: map[string]string{}, Timeout: 60 * time.Second,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	started := false
	var output strings.Builder
	for {
		event, err := stream.Next(ctx)
		if err != nil {
			return "", err
		}
		switch event.Name {
		case "process.start":
			started = true
		case "process.data":
			if value, ok := event.Data["stdout"].(string); ok {
				if output.Len()+len(value) > 1024 {
					return "", errors.New("terminal process output exceeded limit")
				}
				output.WriteString(value)
			}
		case "process.exit":
			if !started || !zeroExit(event.Data) {
				return "", errors.New("terminal process failed")
			}
			value := strings.TrimSpace(output.String())
			if value != "terminal-ok" {
				return "", fmt.Errorf("unexpected terminal output %q", value)
			}
			return value, nil
		}
	}
}

func zeroExit(data map[string]any) bool {
	for _, key := range []string{"exit_code", "exitCode"} {
		switch value := data[key].(type) {
		case json.Number:
			return value.String() == "0"
		case float64:
			return value == 0
		case int:
			return value == 0
		case int64:
			return value == 0
		}
	}
	return false
}

func confirmDeleted(ctx context.Context, control adapter.ControlPlane, sessionID string) bool {
	for {
		requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
		session, err := control.Get(requestContext, sessionID)
		cancel()
		if errors.Is(err, adapter.ErrSessionNotFound) || (err == nil && session.Deleted) {
			return true
		}
		if err != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}
