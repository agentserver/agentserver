package productiondeploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestProductionDeploymentJSONSchemaAcceptsLoaderAndExample(t *testing.T) {
	root := productionRepositoryRoot(t)
	schemaBytes, err := os.ReadFile(filepath.Join(root, "api", "schema", "production-deployment.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("production deployment schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve production deployment schema: %v", err)
	}

	examplePath := filepath.Join(root, "deploy", "production", "config.example.json")
	exampleBytes, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(examplePath); err != nil {
		t.Fatalf("production loader rejected example: %v", err)
	}
	assertProductionSchemaAccepts(t, resolved, exampleBytes)

	fixtureBytes, err := json.Marshal(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig(fixtureBytes); err != nil {
		t.Fatalf("production loader rejected Go fixture: %v", err)
	}
	assertProductionSchemaAccepts(t, resolved, fixtureBytes)

	var openWorld map[string]any
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["future"] = true
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted an unknown top-level field")
	}
	delete(openWorld, "future")
	openWorld["network"].(map[string]any)["future"] = true
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted an unknown nested field")
	}
}

func assertProductionSchemaAccepts(t *testing.T, schema *jsonschema.Resolved, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("production schema rejected loader-valid input: %v", err)
	}
}

func productionRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate productiondeploy package")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
