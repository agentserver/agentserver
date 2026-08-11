package corecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestInternalOpenAPIPathsMatchClientContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source path")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "..", "..", "api", "openapi", "internal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI  string                `json:"openapi"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]struct {
			Get struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"get"`
			Post struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
				Parameters  []struct {
					Name     string `json:"name"`
					In       string `json:"in"`
					Required bool   `json:"required"`
				} `json:"parameters"`
			} `json:"post"`
		} `json:"paths"`
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
			Schemas         map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode internal OpenAPI contract: %v", err)
	}
	if document.OpenAPI != "3.1.0" || len(document.Security) != 1 {
		t.Fatalf("internal OpenAPI identity/security = %q / %+v", document.OpenAPI, document.Security)
	}
	wantPost := map[string]string{
		CompleteExecutorEnrollmentPath:                               "completeExecutorEnrollment",
		AuthorizeExecutorConnectionPath:                              "authorizeExecutorConnection",
		ClaimRunDispatchesPath:                                       "claimRunDispatches",
		CompleteRunDispatchPath("{runDispatchId}"):                   "completeRunDispatch",
		ReleaseRunDispatchPath("{runDispatchId}"):                    "releaseRunDispatch",
		ClaimRunAttemptPath:                                          "claimRunAttempt",
		RenewRunAttemptPath("{runAttemptId}"):                        "renewRunAttempt",
		InterruptRunAttemptPath("{runAttemptId}"):                    "interruptRunAttempt",
		CommitAttemptTerminalPath("{runAttemptId}"):                  "commitAttemptTerminal",
		AbandonRunAttemptPath("{runAttemptId}"):                      "abandonRunAttempt",
		MarkTurnAcceptedPath("{runAttemptId}"):                       "markTurnAccepted",
		BeginRunFinalizationPath("{runAttemptId}"):                   "beginRunFinalization",
		CommitCheckpointPath("{runAttemptId}"):                       "commitCheckpointAndTerminalRun",
		AppendAttemptEventsPath("{runAttemptId}"):                    "appendAttemptEvents",
		ResolveRunLaunchStatePath:                                    "resolveRunLaunchState",
		FreezeBrainToolCatalogPath:                                   "freezeBrainToolCatalog",
		BindBrainThreadCatalogPath("{catalogId}"):                    "bindBrainThreadCatalog",
		AcquireExecutorConnectionPath:                                "acquireExecutorConnection",
		ListExecutorEnvironmentsPath:                                 "listExecutorEnvironments",
		RenewExecutorConnectionPath("{executorId}"):                  "renewExecutorConnection",
		ActivateExecutorConnectionPath("{executorId}"):               "activateExecutorConnection",
		FenceExecutorConnectionPath("{executorId}"):                  "fenceExecutorConnection",
		RecoverExecutorGatewayPath("{executorId}"):                   "recoverExecutorGateway",
		PrepareExecutionPath:                                         "prepareExecution",
		PrepareOperationPath("{executionId}"):                        "prepareOperation",
		BeginOperationDispatchPath("{executionId}", "{operationId}"): "beginOperationDispatch",
		AcknowledgeOperationPath("{executionId}", "{operationId}"):   "acknowledgeOperation",
		CompleteOperationPath("{executionId}", "{operationId}"):      "completeOperation",
		SkipOperationPath("{executionId}", "{operationId}"):          "skipOperation",
		CompleteExecutionPath("{executionId}"):                       "completeExecution",
		CreateApprovalPath:                                           "createApproval",
		ExpireApprovalPath("{approvalId}"):                           "expireApproval",
		CancelApprovalPath("{approvalId}"):                           "cancelApproval",
		ObserveApprovalPath("{approvalId}"):                          "observeApproval",
		ConsumeApprovalPath("{approvalId}"):                          "consumeApprovalAndAuthorizeExecution",
		IssueRunCapabilitiesPath:                                     "issueRunCapabilities",
		AuthorizeExecutorRunCapabilityPath:                           "authorizeExecutorRunCapability",
		AuthorizeLLMProxyRunCapabilityPath:                           "authorizeLLMProxyRunCapability",
		ReserveManagedSandboxPath:                                    "reserveManagedSandbox",
		ListManagedSandboxesForReconcilePath:                         "listManagedSandboxesForReconcile",
		BeginManagedSandboxCreatePath("{sandboxId}"):                 "beginManagedSandboxCreate",
		ObserveManagedSandboxPath("{sandboxId}"):                     "observeManagedSandbox",
		RenewManagedSandboxActivityPath("{sandboxId}"):               "renewManagedSandboxActivity",
		ReleaseManagedSandboxActivityPath("{sandboxId}"):             "releaseManagedSandboxActivity",
		BeginManagedSandboxDeletePath("{sandboxId}"):                 "beginManagedSandboxDelete",
		AuthorizeManagedSandboxOperationPath:                         "authorizeManagedSandboxOperation",
		ResolveEgressCredentialAuthorityPath:                         "resolveEgressCredentialAuthority",
		ResolveEgressCredentialPath:                                  "resolveEgressCredential",
		AuthorizeProcessEnvironmentEgressPath:                        "authorizeProcessEnvironmentEgress",
		ResolveExecutionLarkCredentialPath:                           "resolveExecutionLarkCredential",
		RecordEgressCredentialAuditPath:                              "recordEgressCredentialAuditEvent",
	}
	for path, operationID := range wantPost {
		operation, found := document.Paths[path]
		if !found || operation.Post.OperationID != operationID {
			t.Errorf("internal OpenAPI path %q = %+v, want operationId %q", path, operation, operationID)
		}
	}
	wantGet := map[string]string{
		ManagedSandboxPath("{sandboxId}"): "getManagedSandbox",
	}
	for path, operationID := range wantGet {
		operation, found := document.Paths[path]
		if !found || operation.Get.OperationID != operationID {
			t.Errorf("internal OpenAPI GET path %q = %+v, want operationId %q", path, operation, operationID)
		}
	}
	if len(document.Paths) != len(wantPost)+len(wantGet) {
		t.Fatalf("internal OpenAPI path count = %d, want %d", len(document.Paths), len(wantPost)+len(wantGet))
	}
	if len(document.Components.SecuritySchemes) != 4 || document.Components.SecuritySchemes["workloadMTLS"] == nil ||
		document.Components.SecuritySchemes["runCapabilityBearer"] == nil ||
		document.Components.SecuritySchemes["executorEnrollmentBearer"] == nil ||
		document.Components.SecuritySchemes["executorOAuthBearer"] == nil {
		t.Fatalf("internal OpenAPI security schemes = %v", document.Components.SecuritySchemes)
	}
	for _, path := range []string{AuthorizeExecutorRunCapabilityPath, AuthorizeLLMProxyRunCapabilityPath} {
		security := document.Paths[path].Post.Security
		if len(security) != 1 || security[0]["workloadMTLS"] == nil || security[0]["runCapabilityBearer"] == nil || len(security[0]) != 2 {
			t.Errorf("internal OpenAPI %s security = %+v", path, security)
		}
	}
	for path, bearer := range map[string]string{
		CompleteExecutorEnrollmentPath:  "executorEnrollmentBearer",
		AuthorizeExecutorConnectionPath: "executorOAuthBearer",
	} {
		security := document.Paths[path].Post.Security
		if len(security) != 1 || security[0]["workloadMTLS"] == nil || security[0][bearer] == nil || len(security[0]) != 2 {
			t.Errorf("internal OpenAPI %s security = %+v", path, security)
		}
	}
	enrollmentParameters := document.Paths[CompleteExecutorEnrollmentPath].Post.Parameters
	if len(enrollmentParameters) != 1 || enrollmentParameters[0].Name != ExpectedExecutorIDHeader ||
		enrollmentParameters[0].In != "header" || !enrollmentParameters[0].Required {
		t.Fatalf("internal enrollment deployment binding = %+v", enrollmentParameters)
	}

	assertSchemaFields(t, document.Components.Schemas, "ExecutorResourceState", reflect.TypeFor[ExecutorResourceState]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutorEnrollmentEnvironment", reflect.TypeFor[ExecutorEnrollmentEnvironment]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteExecutorEnrollmentRequest", reflect.TypeFor[CompleteExecutorEnrollmentRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteExecutorEnrollmentResponse", reflect.TypeFor[CompleteExecutorEnrollmentResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeExecutorConnectionResponse", reflect.TypeFor[AuthorizeExecutorConnectionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "EnvironmentDeclaration", reflect.TypeFor[EnvironmentDeclaration]())
	assertSchemaFields(t, document.Components.Schemas, "ConnectionHolder", reflect.TypeFor[ConnectionHolder]())
	assertSchemaFields(t, document.Components.Schemas, "IssueRunCapabilitiesRequest", reflect.TypeFor[IssueRunCapabilitiesRequest]())
	assertSchemaFields(t, document.Components.Schemas, "IssuedRunCapability", reflect.TypeFor[IssuedRunCapability]())
	assertSchemaFields(t, document.Components.Schemas, "IssueRunCapabilitiesResponse", reflect.TypeFor[IssueRunCapabilitiesResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeExecutorRunCapabilityRequest", reflect.TypeFor[AuthorizeExecutorRunCapabilityRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeLLMProxyRunCapabilityRequest", reflect.TypeFor[AuthorizeLLMProxyRunCapabilityRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeRunCapabilityResponse", reflect.TypeFor[AuthorizeRunCapabilityResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeLLMProxyRunCapabilityResponse", reflect.TypeFor[AuthorizeLLMProxyRunCapabilityResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ManagedSandboxState", reflect.TypeFor[ManagedSandboxState]())
	assertSchemaFields(t, document.Components.Schemas, "ReserveManagedSandboxRequest", reflect.TypeFor[ReserveManagedSandboxRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ReserveManagedSandboxResponse", reflect.TypeFor[ReserveManagedSandboxResponse]())
	assertSchemaFields(t, document.Components.Schemas, "GetManagedSandboxResponse", reflect.TypeFor[GetManagedSandboxResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BeginManagedSandboxCreateRequest", reflect.TypeFor[BeginManagedSandboxCreateRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ObserveManagedSandboxRequest", reflect.TypeFor[ObserveManagedSandboxRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RenewManagedSandboxActivityRequest", reflect.TypeFor[RenewManagedSandboxActivityRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ReleaseManagedSandboxActivityRequest", reflect.TypeFor[ReleaseManagedSandboxActivityRequest]())
	assertSchemaFields(t, document.Components.Schemas, "BeginManagedSandboxDeleteRequest", reflect.TypeFor[BeginManagedSandboxDeleteRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ManagedSandboxMutationResponse", reflect.TypeFor[ManagedSandboxMutationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ListManagedSandboxesForReconcileRequest", reflect.TypeFor[ListManagedSandboxesForReconcileRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ListManagedSandboxesForReconcileResponse", reflect.TypeFor[ListManagedSandboxesForReconcileResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeManagedSandboxOperationRequest", reflect.TypeFor[AuthorizeManagedSandboxOperationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeManagedSandboxOperationResponse", reflect.TypeFor[AuthorizeManagedSandboxOperationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "EgressCredentialOperation", reflect.TypeFor[EgressCredentialOperation]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveEgressCredentialAuthorityRequest", reflect.TypeFor[ResolveEgressCredentialAuthorityRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveEgressCredentialAuthorityResponse", reflect.TypeFor[ResolveEgressCredentialAuthorityResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveEgressCredentialRequest", reflect.TypeFor[ResolveEgressCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveEgressCredentialResponse", reflect.TypeFor[ResolveEgressCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveExecutionLarkCredentialRequest", reflect.TypeFor[ResolveExecutionLarkCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveExecutionLarkCredentialResponse", reflect.TypeFor[ResolveExecutionLarkCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AuthorizeProcessEnvironmentEgressRequest", reflect.TypeFor[AuthorizeProcessEnvironmentEgressRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RecordEgressCredentialAuditRequest", reflect.TypeFor[RecordEgressCredentialAuditRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RecordEgressCredentialAuditResponse", reflect.TypeFor[RecordEgressCredentialAuditResponse]())
	var upstreamAuthorizationProperty struct {
		Sensitive bool `json:"x-agentserver-sensitive"`
	}
	if err := json.Unmarshal(document.Components.Schemas["AuthorizeLLMProxyRunCapabilityResponse"].Properties["upstreamAuthorization"], &upstreamAuthorizationProperty); err != nil || !upstreamAuthorizationProperty.Sensitive {
		t.Errorf("llmproxy upstream authorization sensitivity contract = %+v, %v", upstreamAuthorizationProperty, err)
	}
	var credentialHeadersProperty struct {
		Sensitive bool `json:"x-agentserver-sensitive"`
	}
	if err := json.Unmarshal(document.Components.Schemas["ResolveEgressCredentialResponse"].Properties["headers"], &credentialHeadersProperty); err != nil || !credentialHeadersProperty.Sensitive {
		t.Errorf("egress credential header sensitivity contract = %+v, %v", credentialHeadersProperty, err)
	}
	assertSchemaFields(t, document.Components.Schemas, "AcquireExecutorConnectionRequest", reflect.TypeFor[AcquireExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RenewExecutorConnectionRequest", reflect.TypeFor[RenewExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ActivateExecutorConnectionRequest", reflect.TypeFor[ActivateExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "FenceExecutorConnectionRequest", reflect.TypeFor[FenceExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RecoverExecutorGatewayRequest", reflect.TypeFor[RecoverExecutorGatewayRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RecoverExecutorGatewayResponse", reflect.TypeFor[RecoverExecutorGatewayResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutorConnectionResponse", reflect.TypeFor[ExecutorConnectionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ListExecutorEnvironmentsRequest", reflect.TypeFor[ListExecutorEnvironmentsRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutorEnvironment", reflect.TypeFor[ExecutorEnvironment]())
	assertSchemaFields(t, document.Components.Schemas, "ListExecutorEnvironmentsResponse", reflect.TypeFor[ListExecutorEnvironmentsResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ClaimRunDispatchesRequest", reflect.TypeFor[ClaimRunDispatchesRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RunDispatch", reflect.TypeFor[RunDispatch]())
	assertSchemaFields(t, document.Components.Schemas, "ClaimRunDispatchesResponse", reflect.TypeFor[ClaimRunDispatchesResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteRunDispatchRequest", reflect.TypeFor[CompleteRunDispatchRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteRunDispatchResponse", reflect.TypeFor[CompleteRunDispatchResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ReleaseRunDispatchRequest", reflect.TypeFor[ReleaseRunDispatchRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ReleaseRunDispatchResponse", reflect.TypeFor[ReleaseRunDispatchResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RunState", reflect.TypeFor[RunState]())
	assertSchemaFields(t, document.Components.Schemas, "RunAttemptState", reflect.TypeFor[RunAttemptState]())
	assertSchemaFields(t, document.Components.Schemas, "LeaseState", reflect.TypeFor[LeaseState]())
	assertSchemaFields(t, document.Components.Schemas, "ClaimRunAttemptRequest", reflect.TypeFor[ClaimRunAttemptRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ClaimRunAttemptResponse", reflect.TypeFor[ClaimRunAttemptResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RenewRunAttemptRequest", reflect.TypeFor[RenewRunAttemptRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RenewRunAttemptResponse", reflect.TypeFor[RenewRunAttemptResponse]())
	assertSchemaFields(t, document.Components.Schemas, "InterruptRunAttemptRequest", reflect.TypeFor[InterruptRunAttemptRequest]())
	assertSchemaFields(t, document.Components.Schemas, "InterruptRunAttemptResponse", reflect.TypeFor[InterruptRunAttemptResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CommitAttemptTerminalRequest", reflect.TypeFor[CommitAttemptTerminalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CommitAttemptTerminalResponse", reflect.TypeFor[CommitAttemptTerminalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AbandonRunAttemptRequest", reflect.TypeFor[AbandonRunAttemptRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AbandonRunAttemptResponse", reflect.TypeFor[AbandonRunAttemptResponse]())
	assertSchemaFields(t, document.Components.Schemas, "MarkTurnAcceptedRequest", reflect.TypeFor[MarkTurnAcceptedRequest]())
	assertSchemaFields(t, document.Components.Schemas, "MarkTurnAcceptedResponse", reflect.TypeFor[MarkTurnAcceptedResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BeginRunFinalizationRequest", reflect.TypeFor[BeginRunFinalizationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "BeginRunFinalizationResponse", reflect.TypeFor[BeginRunFinalizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CheckpointCommit", reflect.TypeFor[CheckpointCommit]())
	assertSchemaFields(t, document.Components.Schemas, "CommitCheckpointRequest", reflect.TypeFor[CommitCheckpointRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CheckpointState", reflect.TypeFor[CheckpointState]())
	assertSchemaFields(t, document.Components.Schemas, "CommitCheckpointResponse", reflect.TypeFor[CommitCheckpointResponse]())
	assertSchemaFields(t, document.Components.Schemas, "EventObjectPointer", reflect.TypeFor[EventObjectPointer]())
	assertSchemaFields(t, document.Components.Schemas, "AttemptEvent", reflect.TypeFor[AttemptEvent]())
	assertSchemaFields(t, document.Components.Schemas, "AppendAttemptEventsRequest", reflect.TypeFor[AppendAttemptEventsRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AppendedAttemptEvent", reflect.TypeFor[AppendedAttemptEvent]())
	assertSchemaFields(t, document.Components.Schemas, "AppendAttemptEventsResponse", reflect.TypeFor[AppendAttemptEventsResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveRunLaunchStateRequest", reflect.TypeFor[ResolveRunLaunchStateRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RunLaunchObjectPointer", reflect.TypeFor[RunLaunchObjectPointer]())
	assertSchemaFields(t, document.Components.Schemas, "RunLaunchCheckpointState", reflect.TypeFor[RunLaunchCheckpointState]())
	assertSchemaFields(t, document.Components.Schemas, "RunLaunchExecutorPolicyState", reflect.TypeFor[RunLaunchExecutorPolicyState]())
	assertSchemaFields(t, document.Components.Schemas, "RunLaunchLLMGatewayState", reflect.TypeFor[RunLaunchLLMGatewayState]())
	assertSchemaFields(t, document.Components.Schemas, "RunLaunchLarkEgressState", reflect.TypeFor[RunLaunchLarkEgressState]())
	assertSchemaFields(t, document.Components.Schemas, "ResolveRunLaunchStateResponse", reflect.TypeFor[ResolveRunLaunchStateResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BrainToolCatalogState", reflect.TypeFor[BrainToolCatalogState]())
	assertSchemaFields(t, document.Components.Schemas, "FreezeBrainToolCatalogRequest", reflect.TypeFor[FreezeBrainToolCatalogRequest]())
	assertSchemaFields(t, document.Components.Schemas, "FreezeBrainToolCatalogResponse", reflect.TypeFor[FreezeBrainToolCatalogResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BindBrainThreadCatalogRequest", reflect.TypeFor[BindBrainThreadCatalogRequest]())
	assertSchemaFields(t, document.Components.Schemas, "BindBrainThreadCatalogResponse", reflect.TypeFor[BindBrainThreadCatalogResponse]())
	assertSchemaFields(t, document.Components.Schemas, "TransitionRecord", reflect.TypeFor[TransitionRecord]())
	assertSchemaFields(t, document.Components.Schemas, "CanonicalJSONDigest", reflect.TypeFor[CanonicalJSONDigest]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutionState", reflect.TypeFor[ExecutionState]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutionOperationState", reflect.TypeFor[ExecutionOperationState]())
	assertSchemaFields(t, document.Components.Schemas, "PrepareExecutionRequest", reflect.TypeFor[PrepareExecutionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "PrepareExecutionResponse", reflect.TypeFor[PrepareExecutionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "PrepareOperationRequest", reflect.TypeFor[PrepareOperationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "PrepareOperationResponse", reflect.TypeFor[PrepareOperationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BeginOperationDispatchRequest", reflect.TypeFor[BeginOperationDispatchRequest]())
	assertSchemaFields(t, document.Components.Schemas, "BeginOperationDispatchResponse", reflect.TypeFor[BeginOperationDispatchResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AcknowledgeOperationRequest", reflect.TypeFor[AcknowledgeOperationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AcknowledgeOperationResponse", reflect.TypeFor[AcknowledgeOperationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteOperationRequest", reflect.TypeFor[CompleteOperationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteOperationResponse", reflect.TypeFor[CompleteOperationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "SkipOperationRequest", reflect.TypeFor[SkipOperationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "SkipOperationResponse", reflect.TypeFor[SkipOperationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteExecutionRequest", reflect.TypeFor[CompleteExecutionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteExecutionResponse", reflect.TypeFor[CompleteExecutionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ApprovalState", reflect.TypeFor[ApprovalState]())
	assertSchemaFields(t, document.Components.Schemas, "CreateApprovalRequest", reflect.TypeFor[CreateApprovalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CreateApprovalResponse", reflect.TypeFor[CreateApprovalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ApprovalTerminalRequest", reflect.TypeFor[ApprovalTerminalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ApprovalTerminalResponse", reflect.TypeFor[ApprovalTerminalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ConsumeApprovalRequest", reflect.TypeFor[ConsumeApprovalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ConsumeApprovalResponse", reflect.TypeFor[ConsumeApprovalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ObserveApprovalRequest", reflect.TypeFor[ObserveApprovalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ObserveApprovalResponse", reflect.TypeFor[ObserveApprovalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ErrorResponse", reflect.TypeFor[ErrorResponse]())

	wantProfiles := []string{execprofile.Version, execprofile.FilesystemReadVersion}
	for _, schemaName := range []string{"EnvironmentDeclaration", "ExecutorEnvironment"} {
		var property struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(document.Components.Schemas[schemaName].Properties["outerProfileVersion"], &property); err != nil {
			t.Fatalf("decode %s outer profile: %v", schemaName, err)
		}
		if !slices.Equal(property.Enum, wantProfiles) {
			t.Errorf("OpenAPI %s outer profiles = %q, Go profiles = %q", schemaName, property.Enum, wantProfiles)
		}
	}
}

func TestPublicOpenAPIMatchesBrowserRunContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source path")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "..", "..", "api", "openapi", "public.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI  string                `json:"openapi"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]struct {
			Post struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"post"`
			Get struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"get"`
			Patch struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"patch"`
			Delete struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"delete"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
		SecurityFacts struct {
			GatewayReadsPostgreSQL     bool `json:"browserGatewayReadsPostgreSQL"`
			CursorIsAuthorization      bool `json:"cursorIsAuthorization"`
			MembershipRecheckedPerPoll bool `json:"membershipRecheckedPerPoll"`
			RetentionRequiresSnapshot  bool `json:"retentionRequiresLifecycleSnapshot"`
		} `json:"x-agentserver-security"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode public OpenAPI contract: %v", err)
	}
	if document.OpenAPI != "3.1.0" || len(document.Security) != 1 || len(document.Security[0]) != 2 ||
		document.Security[0]["browserGatewayMTLS"] == nil || !slices.Equal(document.Security[0]["browserOAuth"], []string{BrowserOAuthRunsReadScope}) {
		t.Fatalf("public OpenAPI identity/security = %q / %+v", document.OpenAPI, document.Security)
	}
	createPath := CreateUserRunPath("{workspaceId}", "{sessionId}")
	cancelPath := CancelUserRunPath("{workspaceId}", "{runId}")
	readPath := ReadUserRunEventsPath("{workspaceId}", "{runId}")
	decidePath := DecideUserApprovalPath("{workspaceId}", "{approvalId}")
	createExecutorPath := CreateExecutorResourcePath("{workspaceId}")
	issueEnrollmentPath := IssueExecutorEnrollmentTokenPath("{workspaceId}", "{executorId}")
	archiveExecutorPath := ArchiveExecutorResourcePath("{workspaceId}", "{executorId}")
	workspacesPath := WorkspacesPath()
	workspacePath := WorkspacePath("{workspaceId}")
	archiveWorkspacePath := ArchiveWorkspacePath("{workspaceId}")
	membersPath := WorkspaceMembersPath("{workspaceId}")
	memberPath := WorkspaceMemberPath("{workspaceId}", "{memberId}")
	sessionsPath := UserSessionsPath("{workspaceId}")
	sessionPath := UserSessionPath("{workspaceId}", "{sessionId}")
	transcriptPath := UserSessionTranscriptPath("{workspaceId}", "{sessionId}")
	archiveSessionPath := ArchiveUserSessionPath("{workspaceId}", "{sessionId}")
	llmGatewayCollectionPath := WorkspaceLLMGatewaysPath("{workspaceId}")
	llmGatewayResourcePath := WorkspaceLLMGatewayPath("{workspaceId}", "{gatewayId}")
	llmGatewayAuthorizePath := AuthorizeLLMGatewayPath("{workspaceId}", "{gatewayId}")
	llmGatewayCompletePath := CompleteLLMGatewayAuthorizationPath("{workspaceId}", "{gatewayId}")
	llmGatewayRevokePath := RevokeLLMGatewayGrantPath("{workspaceId}", "{gatewayId}")
	llmGatewayDisablePath := DisableLLMGatewayPath("{workspaceId}", "{gatewayId}")
	credentialSchemasPath := WorkspaceCredentialProviderSchemasPath
	credentialCollectionPath := WorkspaceCredentialCollectionRoutePattern
	credentialResourcePath := WorkspaceCredentialResourceRoutePattern
	credentialRotatePath := "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:rotate"
	credentialRevokePath := "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:revoke"
	credentialDeletePath := "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:delete"
	credentialDefaultPath := "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:setDefault"
	credentialAuthorizationCollectionPath := WorkspaceCredentialAuthorizationCollectionRoutePattern
	credentialAuthorizationResourcePath := WorkspaceCredentialAuthorizationResourceRoutePattern
	credentialAuthorizationPollPath := "/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}:poll"
	credentialAuthorizationCancelPath := "/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}:cancel"
	if len(document.Paths) != 33 || document.Paths[createPath].Post.OperationID != "createUserRun" ||
		document.Paths[cancelPath].Post.OperationID != "cancelUserRun" || document.Paths[readPath].Get.OperationID != "readUserRunEvents" {
		t.Fatalf("public OpenAPI paths = %+v", document.Paths)
	}
	if document.Paths[decidePath].Post.OperationID != "decideUserApproval" {
		t.Fatalf("public approval path = %+v", document.Paths[decidePath])
	}
	if document.Paths[sessionsPath].Get.OperationID != "listUserSessions" || document.Paths[sessionsPath].Post.OperationID != "createUserSession" ||
		document.Paths[sessionPath].Get.OperationID != "getUserSession" || document.Paths[sessionPath].Patch.OperationID != "updateUserSession" ||
		document.Paths[transcriptPath].Get.OperationID != "getUserSessionTranscript" ||
		document.Paths[archiveSessionPath].Post.OperationID != "archiveUserSession" {
		t.Fatalf("public Browser session paths = %+v", document.Paths)
	}
	if document.Paths[createExecutorPath].Post.OperationID != "createExecutorResource" ||
		document.Paths[createExecutorPath].Get.OperationID != "listExecutorResources" ||
		document.Paths[issueEnrollmentPath].Post.OperationID != "issueExecutorEnrollmentToken" ||
		document.Paths[archiveExecutorPath].Delete.OperationID != "archiveExecutorResource" {
		t.Fatalf("public executor paths = %+v / %+v", document.Paths[createExecutorPath], document.Paths[issueEnrollmentPath])
	}
	if document.Paths[workspacesPath].Get.OperationID != "listWorkspaces" || document.Paths[workspacesPath].Post.OperationID != "createWorkspace" ||
		document.Paths[workspacePath].Get.OperationID != "getWorkspace" || document.Paths[workspacePath].Patch.OperationID != "updateWorkspace" ||
		document.Paths[archiveWorkspacePath].Post.OperationID != "archiveWorkspace" ||
		document.Paths[membersPath].Get.OperationID != "listWorkspaceMembers" || document.Paths[membersPath].Post.OperationID != "addWorkspaceMember" ||
		document.Paths[memberPath].Patch.OperationID != "updateWorkspaceMember" || document.Paths[memberPath].Delete.OperationID != "removeWorkspaceMember" {
		t.Fatalf("public Platform workspace paths = %+v", document.Paths)
	}
	for _, path := range []string{createExecutorPath, issueEnrollmentPath} {
		security := document.Paths[path].Post.Security
		permission := PlatformOAuthExecutorsCreateScope
		if path == issueEnrollmentPath {
			permission = PlatformOAuthExecutorsEnrollScope
		}
		if len(security) != 1 || len(security[0]) != 2 || security[0]["platformGatewayMTLS"] == nil ||
			!slices.Equal(security[0]["platformOAuth"], []string{permission}) {
			t.Errorf("public executor path %s security = %+v", path, security)
		}
	}
	for _, authority := range []struct {
		security   []map[string][]string
		permission string
	}{
		{document.Paths[createExecutorPath].Get.Security, PlatformOAuthExecutorsReadScope},
		{document.Paths[archiveExecutorPath].Delete.Security, PlatformOAuthExecutorsArchiveScope},
		{document.Paths[workspacesPath].Get.Security, PlatformOAuthWorkspacesReadScope},
		{document.Paths[workspacesPath].Post.Security, PlatformOAuthWorkspacesCreateScope},
		{document.Paths[workspacePath].Get.Security, PlatformOAuthWorkspacesReadScope},
		{document.Paths[workspacePath].Patch.Security, PlatformOAuthWorkspacesUpdateScope},
		{document.Paths[archiveWorkspacePath].Post.Security, PlatformOAuthWorkspacesArchiveScope},
		{document.Paths[membersPath].Get.Security, PlatformOAuthMembersReadScope},
		{document.Paths[membersPath].Post.Security, PlatformOAuthMembersAddScope},
		{document.Paths[memberPath].Patch.Security, PlatformOAuthMembersUpdateScope},
		{document.Paths[memberPath].Delete.Security, PlatformOAuthMembersRemoveScope},
	} {
		if len(authority.security) != 1 || len(authority.security[0]) != 2 || authority.security[0]["platformGatewayMTLS"] == nil ||
			!slices.Equal(authority.security[0]["platformOAuth"], []string{authority.permission}) {
			t.Errorf("public Platform resource security = %+v", authority.security)
		}
	}
	if document.Paths[llmGatewayCollectionPath].Get.OperationID != "listWorkspaceLLMGateways" ||
		document.Paths[llmGatewayCollectionPath].Post.OperationID != "createWorkspaceLLMGateway" ||
		document.Paths[llmGatewayResourcePath].Patch.OperationID != "updateWorkspaceLLMGateway" ||
		document.Paths[llmGatewayAuthorizePath].Post.OperationID != "beginWorkspaceLLMGatewayAuthorization" ||
		document.Paths[llmGatewayCompletePath].Post.OperationID != "completeWorkspaceLLMGatewayAuthorization" ||
		document.Paths[llmGatewayRevokePath].Post.OperationID != "revokeWorkspaceLLMGatewayGrant" ||
		document.Paths[llmGatewayDisablePath].Post.OperationID != "disableWorkspaceLLMGateway" {
		t.Fatalf("public workspace LLM Gateway paths = %+v", document.Paths)
	}
	if document.Paths[credentialSchemasPath].Get.OperationID != "listCredentialProviderSchemas" ||
		document.Paths[credentialCollectionPath].Get.OperationID != "listWorkspaceCredentials" ||
		document.Paths[credentialCollectionPath].Post.OperationID != "createWorkspaceCredential" ||
		document.Paths[credentialResourcePath].Patch.OperationID != "renameWorkspaceCredential" ||
		document.Paths[credentialRotatePath].Post.OperationID != "rotateWorkspaceCredential" ||
		document.Paths[credentialRevokePath].Post.OperationID != "revokeWorkspaceCredential" ||
		document.Paths[credentialDeletePath].Post.OperationID != "deleteWorkspaceCredential" ||
		document.Paths[credentialDefaultPath].Post.OperationID != "setDefaultWorkspaceCredential" ||
		document.Paths[credentialAuthorizationCollectionPath].Post.OperationID != "beginWorkspaceCredentialAuthorization" ||
		document.Paths[credentialAuthorizationResourcePath].Get.OperationID != "getWorkspaceCredentialAuthorization" ||
		document.Paths[credentialAuthorizationPollPath].Post.OperationID != "pollWorkspaceCredentialAuthorization" ||
		document.Paths[credentialAuthorizationCancelPath].Post.OperationID != "cancelWorkspaceCredentialAuthorization" {
		t.Fatalf("public workspace credential paths = %+v", document.Paths)
	}
	for _, authority := range []struct {
		security   []map[string][]string
		permission string
	}{
		{document.Paths[llmGatewayCollectionPath].Get.Security, PlatformOAuthLLMGatewaysReadScope},
		{document.Paths[llmGatewayCollectionPath].Post.Security, PlatformOAuthLLMGatewaysCreateScope},
		{document.Paths[llmGatewayResourcePath].Patch.Security, PlatformOAuthLLMGatewaysUpdateScope},
		{document.Paths[llmGatewayAuthorizePath].Post.Security, PlatformOAuthLLMGrantsAuthorizeScope},
		{document.Paths[llmGatewayCompletePath].Post.Security, PlatformOAuthLLMGrantsAuthorizeScope},
		{document.Paths[llmGatewayRevokePath].Post.Security, PlatformOAuthLLMGrantsRevokeScope},
		{document.Paths[llmGatewayDisablePath].Post.Security, PlatformOAuthLLMGatewaysDisableScope},
	} {
		if len(authority.security) != 1 || len(authority.security[0]) != 2 || authority.security[0]["platformGatewayMTLS"] == nil ||
			!slices.Equal(authority.security[0]["platformOAuth"], []string{authority.permission}) {
			t.Errorf("public workspace LLM Gateway security = %+v", authority.security)
		}
	}
	for _, authority := range []struct {
		security   []map[string][]string
		permission string
	}{
		{document.Paths[credentialSchemasPath].Get.Security, PlatformOAuthCredentialsReadScope},
		{document.Paths[credentialCollectionPath].Get.Security, PlatformOAuthCredentialsReadScope},
		{document.Paths[credentialCollectionPath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialResourcePath].Patch.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialRotatePath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialRevokePath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialDeletePath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialDefaultPath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialAuthorizationCollectionPath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialAuthorizationResourcePath].Get.Security, PlatformOAuthCredentialsReadScope},
		{document.Paths[credentialAuthorizationPollPath].Post.Security, PlatformOAuthCredentialsManageScope},
		{document.Paths[credentialAuthorizationCancelPath].Post.Security, PlatformOAuthCredentialsManageScope},
	} {
		if len(authority.security) != 1 || len(authority.security[0]) != 2 || authority.security[0]["platformGatewayMTLS"] == nil ||
			!slices.Equal(authority.security[0]["platformOAuth"], []string{authority.permission}) {
			t.Errorf("public workspace credential security = %+v", authority.security)
		}
	}
	for _, authority := range []struct {
		security   []map[string][]string
		permission string
	}{
		{document.Paths[createPath].Post.Security, BrowserOAuthRunsCreateScope},
		{document.Paths[cancelPath].Post.Security, BrowserOAuthRunsCancelScope},
		{document.Paths[readPath].Get.Security, BrowserOAuthRunsReadScope},
		{document.Paths[decidePath].Post.Security, BrowserOAuthApprovalsDecideScope},
		{document.Paths[sessionsPath].Get.Security, BrowserOAuthSessionsReadScope},
		{document.Paths[sessionsPath].Post.Security, BrowserOAuthSessionsCreateScope},
		{document.Paths[sessionPath].Get.Security, BrowserOAuthSessionsReadScope},
		{document.Paths[sessionPath].Patch.Security, BrowserOAuthSessionsUpdateScope},
		{document.Paths[archiveSessionPath].Post.Security, BrowserOAuthSessionsArchiveScope},
	} {
		if len(authority.security) != 1 || len(authority.security[0]) != 2 || authority.security[0]["browserGatewayMTLS"] == nil ||
			!slices.Equal(authority.security[0]["browserOAuth"], []string{authority.permission}) {
			t.Errorf("public Browser action security = %+v", authority.security)
		}
	}
	transcriptSecurity := document.Paths[transcriptPath].Get.Security
	if len(transcriptSecurity) != 1 || len(transcriptSecurity[0]) != 2 || transcriptSecurity[0]["browserGatewayMTLS"] == nil ||
		!slices.Equal(transcriptSecurity[0]["browserOAuth"], []string{BrowserOAuthSessionsReadScope, BrowserOAuthRunsReadScope}) {
		t.Errorf("public Browser transcript security = %+v", transcriptSecurity)
	}
	assertSchemaFields(t, document.Components.Schemas, "CreateExecutorResourceRequest", reflect.TypeFor[CreateExecutorResourceRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ExecutorResourceState", reflect.TypeFor[ExecutorResourceState]())
	assertSchemaFields(t, document.Components.Schemas, "CreateExecutorResourceResponse", reflect.TypeFor[CreateExecutorResourceResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ListExecutorResourcesResponse", reflect.TypeFor[ListExecutorResourcesResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ArchiveExecutorResourceResponse", reflect.TypeFor[ArchiveExecutorResourceResponse]())
	assertSchemaFields(t, document.Components.Schemas, "IssueExecutorEnrollmentTokenResponse", reflect.TypeFor[IssueExecutorEnrollmentTokenResponse]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceState", reflect.TypeFor[WorkspaceState]())
	assertSchemaFields(t, document.Components.Schemas, "ListWorkspacesResponse", reflect.TypeFor[ListWorkspacesResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceRequest", reflect.TypeFor[CreateWorkspaceRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceResponse", reflect.TypeFor[CreateWorkspaceResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceRequest", reflect.TypeFor[UpdateWorkspaceRequest]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceResponse", reflect.TypeFor[UpdateWorkspaceResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ArchiveWorkspaceRequest", reflect.TypeFor[ArchiveWorkspaceRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ArchiveWorkspaceResponse", reflect.TypeFor[ArchiveWorkspaceResponse]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceMemberState", reflect.TypeFor[WorkspaceMemberState]())
	assertSchemaFields(t, document.Components.Schemas, "ListWorkspaceMembersResponse", reflect.TypeFor[ListWorkspaceMembersResponse]())
	assertSchemaFields(t, document.Components.Schemas, "AddWorkspaceMemberRequest", reflect.TypeFor[AddWorkspaceMemberRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AddWorkspaceMemberResponse", reflect.TypeFor[AddWorkspaceMemberResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceMemberRequest", reflect.TypeFor[UpdateWorkspaceMemberRequest]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceMemberResponse", reflect.TypeFor[UpdateWorkspaceMemberResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RemoveWorkspaceMemberResponse", reflect.TypeFor[RemoveWorkspaceMemberResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceLLMGatewayRequest", reflect.TypeFor[CreateWorkspaceLLMGatewayRequest]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceLLMGatewayState", reflect.TypeFor[WorkspaceLLMGatewayState]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceLLMGatewayResponse", reflect.TypeFor[CreateWorkspaceLLMGatewayResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceLLMGatewayRequest", reflect.TypeFor[UpdateWorkspaceLLMGatewayRequest]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateWorkspaceLLMGatewayResponse", reflect.TypeFor[UpdateWorkspaceLLMGatewayResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ListWorkspaceLLMGatewaysResponse", reflect.TypeFor[ListWorkspaceLLMGatewaysResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BeginWorkspaceLLMGatewayAuthorizationRequest", reflect.TypeFor[BeginWorkspaceLLMGatewayAuthorizationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "BeginWorkspaceLLMGatewayAuthorizationResponse", reflect.TypeFor[BeginWorkspaceLLMGatewayAuthorizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteWorkspaceLLMGatewayAuthorizationRequest", reflect.TypeFor[CompleteWorkspaceLLMGatewayAuthorizationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CompleteWorkspaceLLMGatewayAuthorizationResponse", reflect.TypeFor[CompleteWorkspaceLLMGatewayAuthorizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RevokeWorkspaceLLMGatewayGrantResponse", reflect.TypeFor[RevokeWorkspaceLLMGatewayGrantResponse]())
	assertSchemaFields(t, document.Components.Schemas, "DisableWorkspaceLLMGatewayResponse", reflect.TypeFor[DisableWorkspaceLLMGatewayResponse]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceCredentialProviderSchema", reflect.TypeFor[WorkspaceCredentialProviderSchema]())
	assertSchemaFields(t, document.Components.Schemas, "ListWorkspaceCredentialProviderSchemasResponse", reflect.TypeFor[ListWorkspaceCredentialProviderSchemasResponse]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceCredentialMetadata", reflect.TypeFor[WorkspaceCredentialMetadata]())
	assertSchemaFields(t, document.Components.Schemas, "ListWorkspaceCredentialsResponse", reflect.TypeFor[ListWorkspaceCredentialsResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceCredentialRequest", reflect.TypeFor[CreateWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CreateWorkspaceCredentialResponse", reflect.TypeFor[CreateWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RotateWorkspaceCredentialRequest", reflect.TypeFor[RotateWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RotateWorkspaceCredentialResponse", reflect.TypeFor[RotateWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RenameWorkspaceCredentialRequest", reflect.TypeFor[RenameWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RenameWorkspaceCredentialResponse", reflect.TypeFor[RenameWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "RevokeWorkspaceCredentialRequest", reflect.TypeFor[RevokeWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RevokeWorkspaceCredentialResponse", reflect.TypeFor[RevokeWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "DeleteWorkspaceCredentialRequest", reflect.TypeFor[DeleteWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "DeleteWorkspaceCredentialResponse", reflect.TypeFor[DeleteWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "SetDefaultWorkspaceCredentialRequest", reflect.TypeFor[SetDefaultWorkspaceCredentialRequest]())
	assertSchemaFields(t, document.Components.Schemas, "SetDefaultWorkspaceCredentialResponse", reflect.TypeFor[SetDefaultWorkspaceCredentialResponse]())
	assertSchemaFields(t, document.Components.Schemas, "BeginWorkspaceCredentialAuthorizationRequest", reflect.TypeFor[BeginWorkspaceCredentialAuthorizationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "WorkspaceCredentialAuthorization", reflect.TypeFor[WorkspaceCredentialAuthorization]())
	assertSchemaFields(t, document.Components.Schemas, "BeginWorkspaceCredentialAuthorizationResponse", reflect.TypeFor[BeginWorkspaceCredentialAuthorizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "GetWorkspaceCredentialAuthorizationResponse", reflect.TypeFor[GetWorkspaceCredentialAuthorizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "PollWorkspaceCredentialAuthorizationResponse", reflect.TypeFor[PollWorkspaceCredentialAuthorizationResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CancelWorkspaceCredentialAuthorizationRequest", reflect.TypeFor[CancelWorkspaceCredentialAuthorizationRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CancelWorkspaceCredentialAuthorizationResponse", reflect.TypeFor[CancelWorkspaceCredentialAuthorizationResponse]())
	var rawDocument struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &rawDocument); err != nil {
		t.Fatalf("decode public OpenAPI raw credential schema: %v", err)
	}
	var workspaceCredentialSecret struct {
		WriteOnly bool `json:"writeOnly"`
		Sensitive bool `json:"x-agentserver-sensitive"`
	}
	if err := json.Unmarshal(rawDocument.Components.Schemas["WorkspaceCredentialSecret"], &workspaceCredentialSecret); err != nil ||
		!workspaceCredentialSecret.WriteOnly || !workspaceCredentialSecret.Sensitive {
		t.Errorf("workspace credential secret sensitivity contract = %+v, %v", workspaceCredentialSecret, err)
	}
	assertSchemaFields(t, document.Components.Schemas, "UserSessionState", reflect.TypeFor[UserSessionState]())
	assertSchemaFields(t, document.Components.Schemas, "ListUserSessionsResponse", reflect.TypeFor[ListUserSessionsResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CreateUserSessionRequest", reflect.TypeFor[CreateUserSessionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CreateUserSessionResponse", reflect.TypeFor[CreateUserSessionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateUserSessionRequest", reflect.TypeFor[UpdateUserSessionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "UpdateUserSessionResponse", reflect.TypeFor[UpdateUserSessionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ArchiveUserSessionRequest", reflect.TypeFor[ArchiveUserSessionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ArchiveUserSessionResponse", reflect.TypeFor[ArchiveUserSessionResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UserSessionTranscriptMessage", reflect.TypeFor[UserSessionTranscriptMessage]())
	assertSchemaFields(t, document.Components.Schemas, "GetUserSessionTranscriptResponse", reflect.TypeFor[GetUserSessionTranscriptResponse]())
	var enrollmentTokenProperty struct {
		WriteOnly bool `json:"writeOnly"`
		Sensitive bool `json:"x-agentserver-sensitive"`
	}
	if err := json.Unmarshal(document.Components.Schemas["IssueExecutorEnrollmentTokenResponse"].Properties["token"], &enrollmentTokenProperty); err != nil ||
		enrollmentTokenProperty.WriteOnly || !enrollmentTokenProperty.Sensitive {
		t.Errorf("public enrollment token response sensitivity contract = %+v, %v", enrollmentTokenProperty, err)
	}
	assertSchemaFields(t, document.Components.Schemas, "CreateUserRunRequest", reflect.TypeFor[CreateUserRunRequest]())
	assertSchemaFields(t, document.Components.Schemas, "CreateUserRunResponse", reflect.TypeFor[CreateUserRunResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CancelUserRunResponse", reflect.TypeFor[CancelUserRunResponse]())
	assertSchemaFields(t, document.Components.Schemas, "CanonicalJSONDigest", reflect.TypeFor[CanonicalJSONDigest]())
	assertSchemaFields(t, document.Components.Schemas, "ApprovalState", reflect.TypeFor[ApprovalState]())
	assertSchemaFields(t, document.Components.Schemas, "DecideUserApprovalRequest", reflect.TypeFor[DecideUserApprovalRequest]())
	assertSchemaFields(t, document.Components.Schemas, "DecideUserApprovalResponse", reflect.TypeFor[DecideUserApprovalResponse]())
	assertSchemaFields(t, document.Components.Schemas, "ReadUserRunEventsResponse", reflect.TypeFor[ReadUserRunEventsResponse]())
	assertSchemaFields(t, document.Components.Schemas, "UserRunSnapshot", reflect.TypeFor[UserRunSnapshot]())
	assertSchemaFields(t, document.Components.Schemas, "UserRunCursorExpiredResponse", reflect.TypeFor[UserRunCursorExpiredResponse]())
	assertSchemaFields(t, document.Components.Schemas, "PublicErrorResponse", reflect.TypeFor[PublicErrorResponse]())
	if document.SecurityFacts.GatewayReadsPostgreSQL || document.SecurityFacts.CursorIsAuthorization ||
		!document.SecurityFacts.MembershipRecheckedPerPoll || !document.SecurityFacts.RetentionRequiresSnapshot {
		t.Fatalf("public OpenAPI security facts = %+v", document.SecurityFacts)
	}
}

func assertSchemaFields(t *testing.T, schemas map[string]struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}, name string, goType reflect.Type) {
	t.Helper()
	schema, found := schemas[name]
	if !found {
		t.Fatalf("OpenAPI schema %q is missing", name)
	}
	properties := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		properties = append(properties, field)
	}
	sort.Strings(properties)
	var goFields []string
	var goRequired []string
	collectJSONFields(goType, &goFields, &goRequired)
	sort.Strings(goFields)
	sort.Strings(goRequired)
	sort.Strings(schema.Required)
	if !slices.Equal(properties, goFields) {
		t.Errorf("OpenAPI %s properties = %v, Go JSON fields = %v", name, properties, goFields)
	}
	if !slices.Equal(schema.Required, goRequired) {
		t.Errorf("OpenAPI %s required = %v, Go required fields = %v", name, schema.Required, goRequired)
	}
}

func collectJSONFields(goType reflect.Type, fields, required *[]string) {
	for index := range goType.NumField() {
		field := goType.Field(index)
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] == "-" {
			continue
		}
		if parts[0] == "" {
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				collectJSONFields(field.Type, fields, required)
			}
			continue
		}
		*fields = append(*fields, parts[0])
		if !slices.Contains(parts[1:], "omitempty") {
			*required = append(*required, parts[0])
		}
	}
}
