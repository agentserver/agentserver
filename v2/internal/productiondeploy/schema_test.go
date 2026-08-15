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
	delete(openWorld, "sandboxRegions")
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted a version 6 document without sandboxRegions")
	}
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["sandboxRegions"].(map[string]any)["defaultRegion"] = "cn"
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted a noncanonical workspace initial sandbox region")
	}
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["sandboxRegions"].(map[string]any)["regions"] = []any{"cn"}
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted a catalog without the workspace initial sandbox region")
	}
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["sandboxProfiles"].([]any)[0].(map[string]any)["region"] = "cn"
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted profiles without the workspace initial sandbox region")
	}
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	managedTAE := openWorld["managedExecutor"].(map[string]any)["tae"].(map[string]any)
	delete(managedTAE, "proxyProfile")
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted a version 6 managed TAE authority without proxyProfile")
	}
	if err := json.Unmarshal(exampleBytes, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["version"] = float64(LegacyVersion)
	delete(openWorld, "sandboxRegions")
	delete(openWorld, "sandboxProfiles")
	delete(openWorld, "proxyProfiles")
	legacyTAE := openWorld["managedExecutor"].(map[string]any)["tae"].(map[string]any)
	legacyTAE["region"] = ProductionRegion
	for _, name := range []string{"controlPlaneUrl", "dataPlaneSuffix", "bytecloudSite", "bytecloudJwtEndpoint", "proxyProfile"} {
		delete(legacyTAE, name)
	}
	if err := resolved.Validate(openWorld); err != nil {
		t.Fatalf("production schema rejected a compatible raw version 5 document: %v", err)
	}
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
	delete(openWorld["network"].(map[string]any), "future")
	openWorld["managedExecutor"].(map[string]any)["lark"].(map[string]any)["credentialMode"] = "process_env"
	if err := resolved.Validate(openWorld); err == nil {
		t.Fatal("production schema accepted a deployment-wide managed Lark credential mode")
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
