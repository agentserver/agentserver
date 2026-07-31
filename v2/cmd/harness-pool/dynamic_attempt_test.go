package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	dynamicTestWorkspaceID = "91000000-0000-4000-8000-000000000001"
	dynamicTestSessionID   = "92000000-0000-4000-8000-000000000002"
	dynamicTestRunID       = "93000000-0000-4000-8000-000000000003"
	dynamicTestActorID     = "94000000-0000-4000-8000-000000000004"
	dynamicTestDispatchID  = "95000000-0000-4000-8000-000000000005"
	dynamicTestPromptID    = "96000000-0000-4000-8000-000000000006"
)

func TestServeHarnessPoolIssuesDynamicCapabilitiesForClaimedAttempt(t *testing.T) {
	configuration := validHarnessPoolConfiguration(t)
	workerConfig := configuration[poolWorkerConfigEnvironment]
	capturePath := workerConfig + ".capture"
	workerScript := filepath.Join(filepath.Dir(workerConfig), "capture-worker.sh")
	script := "#!/bin/sh\n" +
		"capture=\"${2#--config=}\".capture\n" +
		"cat <&3 > \"$capture\"\n" +
		"cat <&4 >/dev/null\n" +
		"exit 23\n"
	if err := os.WriteFile(workerScript, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(workerScript, 0o700); err != nil {
		t.Fatal(err)
	}
	configuration[poolWorkerExecutableEnvironment] = workerScript

	fixture := newHarnessPoolServeTLSFixture(t, configuration)
	prepareHarnessPoolServeFiles(t, configuration, fixture)
	prompt := []byte("capture the per-attempt development bootstrap")
	promptDigest := sha256.Sum256(prompt)
	if err := os.WriteFile(
		filepath.Join(configuration[poolDevObjectRootEnvironment], dynamicTestPromptID+".prompt"),
		prompt, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	core := newDynamicAttemptCore(promptDigest, int64(len(prompt)))
	stopCore, coreURL := startHarnessPoolTestCore(t, fixture, core.ServeHTTP)
	defer stopCore()
	configuration[poolCoreURLEnvironment] = coreURL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveHarnessPool(ctx, func(name string) string { return configuration[name] }, io.Discard, io.Discard)
	}()
	select {
	case <-core.released:
	case <-time.After(harnessPoolServeIntegrationTimeout):
		t.Fatal("synthetic attempt was not launched and released")
	}
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := harnessbootstrap.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest := envelope.SignedManifest
	var signedManifest struct {
		WorkspaceID          string `json:"workspaceId"`
		SessionID            string `json:"sessionId"`
		RunID                string `json:"runId"`
		RunAttemptID         string `json:"runAttemptId"`
		RunAttemptGeneration int64  `json:"runAttemptGeneration"`
		HolderID             string `json:"holderId"`
		ExecutorMCP          struct {
			CatalogDigest string `json:"catalogDigest"`
		} `json:"executorMcp"`
	}
	if err := json.Unmarshal(manifest.Manifest, &signedManifest); err != nil {
		t.Fatal(err)
	}
	if signedManifest.WorkspaceID != dynamicTestWorkspaceID || signedManifest.SessionID != dynamicTestSessionID ||
		signedManifest.RunID != dynamicTestRunID || signedManifest.RunAttemptID == "" ||
		signedManifest.RunAttemptGeneration != 1 || signedManifest.HolderID == "" {
		t.Fatalf("captured signed manifest authority = %+v", signedManifest)
	}

	codec, err := runcapability.NewDevelopmentCodecFromBase64Key(configuration[poolDevRunCapabilityKeyEnvironment])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	executor, err := codec.Verify(envelope.RuntimeCapabilities.ExecutorMCP, runcapability.AudienceExecutorMCP, now)
	if err != nil {
		t.Fatal(err)
	}
	model, err := codec.Verify(envelope.RuntimeCapabilities.LLMProxy, runcapability.AudienceLLMProxy, now)
	if err != nil {
		t.Fatal(err)
	}
	if executor.WorkspaceID != dynamicTestWorkspaceID || executor.SessionID != dynamicTestSessionID ||
		executor.RunID != dynamicTestRunID || executor.RunAttemptID != signedManifest.RunAttemptID ||
		executor.RunAttemptGeneration != 1 || executor.ActorID != dynamicTestActorID ||
		executor.HolderID != signedManifest.HolderID || executor.ExecutorID != configuration[poolDevExecutorIDEnvironment] ||
		executor.ToolCatalogDigest != signedManifest.ExecutorMCP.CatalogDigest ||
		executor.ExpectedRunVersion != 3 || executor.ExpectedRunAttemptVersion != 2 {
		t.Fatalf("captured executor capability = %+v", executor)
	}
	if model.CapabilityID == executor.CapabilityID || model.RunID != executor.RunID ||
		model.RunAttemptID != executor.RunAttemptID || model.ActorID != executor.ActorID ||
		model.HolderID != executor.HolderID || model.Model != configuration[poolModelEnvironment] ||
		model.Provider != configuration[poolModelProviderEnvironment] || model.ExecutorID != "" ||
		model.ToolCatalogDigest != "" || model.ExpectedRunVersion != 0 || model.ExpectedRunAttemptVersion != 0 {
		t.Fatalf("captured model capability = %+v", model)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(harnessPoolServeIntegrationTimeout):
		t.Fatal("harness-pool did not stop after the dynamic attempt test")
	}
}

type dynamicAttemptCore struct {
	mu              sync.Mutex
	dispatchClaimed bool
	holderID        string
	attemptID       string
	now             time.Time
	promptDigest    [sha256.Size]byte
	promptSize      int64
	releaseOnce     sync.Once
	released        chan struct{}
}

func newDynamicAttemptCore(promptDigest [sha256.Size]byte, promptSize int64) *dynamicAttemptCore {
	return &dynamicAttemptCore{
		now: time.Now().UTC(), promptDigest: promptDigest, promptSize: promptSize,
		released: make(chan struct{}),
	}
}

func (core *dynamicAttemptCore) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	switch {
	case request.URL.Path == corecontract.ClaimRunDispatchesPath:
		core.claimDispatch(response, request)
	case request.URL.Path == corecontract.ClaimRunAttemptPath:
		core.claimAttempt(response, request)
	case request.URL.Path == corecontract.ResolveRunLaunchStatePath:
		core.resolveLaunch(response, request)
	case request.URL.Path == corecontract.FreezeBrainToolCatalogPath:
		core.freezeCatalog(response, request)
	case strings.HasPrefix(request.URL.Path, corecontract.RunDispatchPathPrefix) && strings.HasSuffix(request.URL.Path, ":release"):
		core.releaseDispatch(response, request)
	default:
		http.Error(response, "unexpected synthetic Core command", http.StatusNotFound)
	}
}

func (core *dynamicAttemptCore) claimDispatch(response http.ResponseWriter, request *http.Request) {
	var command corecontract.ClaimRunDispatchesRequest
	if !decodeDynamicCoreRequest(response, request, &command) {
		return
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	result := corecontract.ClaimRunDispatchesResponse{}
	if !core.dispatchClaimed {
		core.dispatchClaimed = true
		core.holderID = command.OwnerID
		result.RunDispatches = []corecontract.RunDispatch{{
			RunDispatchID: dynamicTestDispatchID, WorkspaceID: dynamicTestWorkspaceID,
			SessionID: dynamicTestSessionID, RunID: dynamicTestRunID,
			EnqueuedRunVersion: 1, CurrentRunVersion: 1, CurrentRunStatus: "queued",
			ClaimOwnerID: command.OwnerID, ClaimGeneration: 1,
			AvailableAt: core.now, LockExpiresAt: core.now.Add(time.Minute), CreatedAt: core.now,
		}}
	}
	writeDynamicCoreResponse(response, result)
}

func (core *dynamicAttemptCore) claimAttempt(response http.ResponseWriter, request *http.Request) {
	var command corecontract.ClaimRunAttemptRequest
	if !decodeDynamicCoreRequest(response, request, &command) {
		return
	}
	core.mu.Lock()
	core.attemptID = command.RunAttemptID
	core.holderID = command.HolderID
	core.mu.Unlock()
	lease := corecontract.LeaseState{
		HolderID: command.HolderID, Generation: 1, ExpiresAt: core.now.Add(time.Minute),
		AcquiredAt: core.now, RenewedAt: core.now,
	}
	writeDynamicCoreResponse(response, corecontract.ClaimRunAttemptResponse{
		Run: corecontract.RunState{
			RunID: dynamicTestRunID, WorkspaceID: dynamicTestWorkspaceID, SessionID: dynamicTestSessionID,
			ActorID: dynamicTestActorID, Status: "starting", CurrentAttemptGeneration: 1,
			NextEventSeq: 2, Version: 2, CreatedAt: core.now, UpdatedAt: core.now,
		},
		RunAttempt: corecontract.RunAttemptState{
			RunAttemptID: command.RunAttemptID, RunID: dynamicTestRunID, Generation: 1,
			Status: "leased", HolderID: command.HolderID, Version: 1,
			CreatedAt: core.now, UpdatedAt: core.now,
		},
		SessionLease: lease, AttemptLease: lease, Created: true,
	})
}

func (core *dynamicAttemptCore) resolveLaunch(response http.ResponseWriter, request *http.Request) {
	var command corecontract.ResolveRunLaunchStateRequest
	if !decodeDynamicCoreRequest(response, request, &command) {
		return
	}
	writeDynamicCoreResponse(response, corecontract.ResolveRunLaunchStateResponse{
		WorkspaceID: command.WorkspaceID, SessionID: command.SessionID, RunID: command.RunID,
		RunAttemptID: command.RunAttemptID, HolderID: command.HolderID,
		RunAttemptGeneration: command.RunAttemptGeneration,
		RunVersion:           command.ExpectedRunVersion, RunAttemptVersion: command.ExpectedRunAttemptVersion,
		Prompt: corecontract.RunLaunchObjectPointer{
			ObjectID: dynamicTestPromptID, SHA256: hex.EncodeToString(core.promptDigest[:]),
			SizeBytes: core.promptSize, MediaType: "text/plain; charset=utf-8",
		},
		ExecutorPolicy: corecontract.RunLaunchExecutorPolicyState{
			Version: "dynamic-test-policy", ContextDigest: strings.Repeat("e", 64), AllowedTools: []string{},
		},
	})
}

func (core *dynamicAttemptCore) freezeCatalog(response http.ResponseWriter, request *http.Request) {
	var command corecontract.FreezeBrainToolCatalogRequest
	if !decodeDynamicCoreRequest(response, request, &command) {
		return
	}
	writeDynamicCoreResponse(response, corecontract.FreezeBrainToolCatalogResponse{
		Catalog: corecontract.BrainToolCatalogState{
			CatalogID: command.CatalogID, WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
			CreatedRunID: command.RunID, CreatedRunAttemptID: command.RunAttemptID,
			CreatedAttemptGeneration: command.RunAttemptGeneration, CreatedHolderID: command.HolderID,
			CreatedRunVersion: command.ExpectedRunVersion, CreatedAttemptVersion: command.ExpectedRunAttemptVersion,
			ContractVersion: command.ContractVersion, CanonicalizerVersion: command.CanonicalizerVersion,
			CanonicalCatalog: append(json.RawMessage(nil), command.CanonicalCatalog...), CatalogDigest: command.CatalogDigest,
			PolicyVersion: command.PolicyVersion, PolicyContextDigest: command.PolicyContextDigest,
			Version: 1, CreatedAt: core.now, UpdatedAt: core.now,
		},
		Created: true,
	})
}

func (core *dynamicAttemptCore) releaseDispatch(response http.ResponseWriter, request *http.Request) {
	var command corecontract.ReleaseRunDispatchRequest
	if !decodeDynamicCoreRequest(response, request, &command) {
		return
	}
	writeDynamicCoreResponse(response, corecontract.ReleaseRunDispatchResponse{Released: true})
	core.releaseOnce.Do(func() { close(core.released) })
}

func decodeDynamicCoreRequest(response http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		http.Error(response, "invalid synthetic Core request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeDynamicCoreResponse(response http.ResponseWriter, value any) {
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(err)
	}
}
