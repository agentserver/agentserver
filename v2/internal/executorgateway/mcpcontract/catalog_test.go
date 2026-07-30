package mcpcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestCatalogIsTheMinimalShellSlice(t *testing.T) {
	got := Tools()
	if len(got) != 2 {
		t.Fatalf("tool count = %d, want 2", len(got))
	}
	if names := []string{got[0].Name, got[1].Name}; !slices.Equal(names, []string{"list_environments", "shell"}) {
		t.Fatalf("tool names = %q", names)
	}
	if _, found := Lookup("unified_exec"); found {
		t.Fatal("unimplemented long-lived process tool leaked into shell-v1 catalog")
	}
	if _, found := Lookup("process/signal"); found {
		t.Fatal("stock method leaked into model-visible tool names")
	}

	descriptors := make([]harnessworker.ToolDescriptor, 0, len(got))
	for _, tool := range got {
		descriptors = append(descriptors, harnessworker.ToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	if _, err := harnessworker.BuildCatalog(Namespace, NamespaceDescription, descriptors, harnessworker.DefaultLimits()); err != nil {
		t.Fatalf("BuildCatalog() rejected executor MCP contract: %v", err)
	}
}

func TestToolsAndLookupReturnDefensiveCopies(t *testing.T) {
	first := Tools()
	first[0].InputSchema[0] = 'x'
	first[0].OutputSchema[0] = 'x'
	first[0].Name = "changed"
	second := Tools()
	if second[0].Name != ToolListEnvironments || second[0].InputSchema[0] != '{' || second[0].OutputSchema[0] != '{' {
		t.Fatal("Tools exposed mutable catalog storage")
	}
	tool, found := Lookup(ToolShell)
	if !found {
		t.Fatal("Lookup(shell) did not find the frozen tool")
	}
	tool.InputSchema[0] = 'x'
	again, _ := Lookup(ToolShell)
	if again.InputSchema[0] != '{' {
		t.Fatal("Lookup exposed mutable schema storage")
	}
}

func TestExecutorMCPSchemaMatchesGoCatalogAndValidatesCalls(t *testing.T) {
	document := loadContractSchema(t)
	if document.ContractVersion != Version || document.Namespace != Namespace {
		t.Fatalf("schema contract = %q/%q, want %q/%q", document.ContractVersion, document.Namespace, Version, Namespace)
	}
	if len(document.Tools) != len(tools) {
		t.Fatalf("schema tools = %d, Go tools = %d", len(document.Tools), len(tools))
	}
	for index, want := range Tools() {
		got := document.Tools[index]
		if got.Name != want.Name || got.Description != want.Description || got.MapperVersion != want.MapperVersion {
			t.Fatalf("schema tool %d metadata = %+v, want %+v", index, got, want)
		}
		assertSchemaEquivalent(t, document.Raw, got.InputSchema, want.InputSchema)
		assertSchemaEquivalent(t, document.Raw, got.OutputSchema, want.OutputSchema)
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(document.Raw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve executor MCP schema: %v", err)
	}
	valid := []string{
		`{"name":"list_environments","arguments":{}}`,
		`{"name":"list_environments","arguments":{"executor_id":"10000000-0000-0000-0000-000000000001"}}`,
		`{"name":"shell","arguments":{"environment_id":"20000000-0000-0000-0000-000000000002","argv":["/bin/sh","-lc","pwd"],"cwd":".","env":{"LANG":"C"},"timeout_ms":1000,"tty":false}}`,
	}
	for _, raw := range valid {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Fatalf("valid call %s rejected: %v", raw, err)
		}
	}
	invalid := []string{
		`{"name":"shell","arguments":{"environment_id":"not-a-uuid","argv":["pwd"]}}`,
		`{"name":"shell","arguments":{"environment_id":"20000000-0000-0000-0000-000000000002","command":"pwd"}}`,
		`{"name":"shell","arguments":{"environment_id":"20000000-0000-0000-0000-000000000002","argv":[],"future":true}}`,
		`{"name":"unified_exec","arguments":{}}`,
	}
	for _, raw := range invalid {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err == nil {
			t.Fatalf("invalid call %s was accepted", raw)
		}
	}
}

type contractDocument struct {
	Raw             json.RawMessage
	ContractVersion string         `json:"x-agentserver-contract-version"`
	Namespace       string         `json:"x-agentserver-namespace"`
	Tools           []contractTool `json:"x-agentserver-tools"`
}

type contractTool struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	MapperVersion string          `json:"mapperVersion"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	OutputSchema  json.RawMessage `json:"outputSchema"`
}

func loadContractSchema(t *testing.T) contractDocument {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate mcpcontract package")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "api", "schema", "executor-mcp.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document contractDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("executor MCP schema is not valid JSON: %v", err)
	}
	document.Raw = raw
	return document
}

func assertSchemaEquivalent(t *testing.T, document, reference, wantRaw json.RawMessage) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(reference, &got); err != nil {
		t.Fatal(err)
	}
	got, err := dereferenceLocal(root, got, 0)
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("schema drift:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func dereferenceLocal(root map[string]any, value any, depth int) (any, error) {
	if depth > 64 {
		return nil, &schemaReferenceError{"local schema reference nesting exceeds 64"}
	}
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			resolved, err := dereferenceLocal(root, child, depth+1)
			if err != nil {
				return nil, err
			}
			result[index] = resolved
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		if reference, ok := typed["$ref"].(string); ok {
			const prefix = "#/$defs/"
			if !strings.HasPrefix(reference, prefix) || strings.Contains(strings.TrimPrefix(reference, prefix), "/") {
				return nil, &schemaReferenceError{"unsupported schema reference " + reference}
			}
			definitions, ok := root["$defs"].(map[string]any)
			if !ok {
				return nil, &schemaReferenceError{"schema has no $defs"}
			}
			target, found := definitions[strings.TrimPrefix(reference, prefix)]
			if !found {
				return nil, &schemaReferenceError{"missing schema reference " + reference}
			}
			resolved, err := dereferenceLocal(root, target, depth+1)
			if err != nil {
				return nil, err
			}
			for key, child := range resolved.(map[string]any) {
				result[key] = child
			}
		}
		for key, child := range typed {
			if key == "$ref" {
				continue
			}
			resolved, err := dereferenceLocal(root, child, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

type schemaReferenceError struct{ message string }

func (e *schemaReferenceError) Error() string { return e.message }
