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
			Post struct {
				OperationID string `json:"operationId"`
			} `json:"post"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
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
	want := map[string]string{
		ClaimRunDispatchesPath:                                       "claimRunDispatches",
		CompleteRunDispatchPath("{runDispatchId}"):                   "completeRunDispatch",
		ReleaseRunDispatchPath("{runDispatchId}"):                    "releaseRunDispatch",
		ClaimRunAttemptPath:                                          "claimRunAttempt",
		RenewRunAttemptPath("{runAttemptId}"):                        "renewRunAttempt",
		MarkTurnAcceptedPath("{runAttemptId}"):                       "markTurnAccepted",
		AppendAttemptEventsPath("{runAttemptId}"):                    "appendAttemptEvents",
		AcquireExecutorConnectionPath:                                "acquireExecutorConnection",
		ListExecutorEnvironmentsPath:                                 "listExecutorEnvironments",
		RenewExecutorConnectionPath("{executorId}"):                  "renewExecutorConnection",
		ActivateExecutorConnectionPath("{executorId}"):               "activateExecutorConnection",
		FenceExecutorConnectionPath("{executorId}"):                  "fenceExecutorConnection",
		PrepareExecutionPath:                                         "prepareExecution",
		PrepareOperationPath("{executionId}"):                        "prepareOperation",
		BeginOperationDispatchPath("{executionId}", "{operationId}"): "beginOperationDispatch",
		AcknowledgeOperationPath("{executionId}", "{operationId}"):   "acknowledgeOperation",
		CompleteOperationPath("{executionId}", "{operationId}"):      "completeOperation",
		SkipOperationPath("{executionId}", "{operationId}"):          "skipOperation",
		CompleteExecutionPath("{executionId}"):                       "completeExecution",
	}
	for path, operationID := range want {
		operation, found := document.Paths[path]
		if !found || operation.Post.OperationID != operationID {
			t.Errorf("internal OpenAPI path %q = %+v, want operationId %q", path, operation, operationID)
		}
	}
	if len(document.Paths) != len(want) {
		t.Fatalf("internal OpenAPI path count = %d, want %d", len(document.Paths), len(want))
	}

	assertSchemaFields(t, document.Components.Schemas, "EnvironmentDeclaration", reflect.TypeFor[EnvironmentDeclaration]())
	assertSchemaFields(t, document.Components.Schemas, "ConnectionHolder", reflect.TypeFor[ConnectionHolder]())
	assertSchemaFields(t, document.Components.Schemas, "AcquireExecutorConnectionRequest", reflect.TypeFor[AcquireExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "RenewExecutorConnectionRequest", reflect.TypeFor[RenewExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "ActivateExecutorConnectionRequest", reflect.TypeFor[ActivateExecutorConnectionRequest]())
	assertSchemaFields(t, document.Components.Schemas, "FenceExecutorConnectionRequest", reflect.TypeFor[FenceExecutorConnectionRequest]())
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
	assertSchemaFields(t, document.Components.Schemas, "MarkTurnAcceptedRequest", reflect.TypeFor[MarkTurnAcceptedRequest]())
	assertSchemaFields(t, document.Components.Schemas, "MarkTurnAcceptedResponse", reflect.TypeFor[MarkTurnAcceptedResponse]())
	assertSchemaFields(t, document.Components.Schemas, "EventObjectPointer", reflect.TypeFor[EventObjectPointer]())
	assertSchemaFields(t, document.Components.Schemas, "AttemptEvent", reflect.TypeFor[AttemptEvent]())
	assertSchemaFields(t, document.Components.Schemas, "AppendAttemptEventsRequest", reflect.TypeFor[AppendAttemptEventsRequest]())
	assertSchemaFields(t, document.Components.Schemas, "AppendedAttemptEvent", reflect.TypeFor[AppendedAttemptEvent]())
	assertSchemaFields(t, document.Components.Schemas, "AppendAttemptEventsResponse", reflect.TypeFor[AppendAttemptEventsResponse]())
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
	for index := range goType.NumField() {
		tag := goType.Field(index).Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		goFields = append(goFields, parts[0])
		if !slices.Contains(parts[1:], "omitempty") {
			goRequired = append(goRequired, parts[0])
		}
	}
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
