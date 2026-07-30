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
)

func TestInternalOpenAPIExecutorConnectionPathsMatchClientContract(t *testing.T) {
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
		AcquireExecutorConnectionPath:                  "acquireExecutorConnection",
		ListExecutorEnvironmentsPath:                   "listExecutorEnvironments",
		RenewExecutorConnectionPath("{executorId}"):    "renewExecutorConnection",
		ActivateExecutorConnectionPath("{executorId}"): "activateExecutorConnection",
		FenceExecutorConnectionPath("{executorId}"):    "fenceExecutorConnection",
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
	assertSchemaFields(t, document.Components.Schemas, "ErrorResponse", reflect.TypeFor[ErrorResponse]())
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
