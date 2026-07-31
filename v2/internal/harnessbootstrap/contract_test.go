package harnessbootstrap

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestBootstrapSchemaResolvesRunManifestAndMatchesProtocol(t *testing.T) {
	schemaDirectory := bootstrapSchemaDirectory(t)
	bootstrapRaw, err := os.ReadFile(filepath.Join(schemaDirectory, "harness-bootstrap.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(bootstrapRaw, &schema); err != nil {
		t.Fatalf("harness bootstrap schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{Loader: func(uri *url.URL) (*jsonschema.Schema, error) {
		if filepath.Base(uri.Path) != "run-manifest.schema.json" {
			return nil, fmt.Errorf("unexpected external schema %q", uri)
		}
		raw, readErr := os.ReadFile(filepath.Join(schemaDirectory, "run-manifest.schema.json"))
		if readErr != nil {
			return nil, readErr
		}
		var dependency jsonschema.Schema
		if unmarshalErr := json.Unmarshal(raw, &dependency); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return &dependency, nil
	}})
	if err != nil {
		t.Fatalf("resolve harness bootstrap schema: %v", err)
	}
	if resolved.Schema().ID != "https://agentserver.dev/v2/schema/harness-bootstrap.schema.json" {
		t.Fatalf("bootstrap schema ID = %q", resolved.Schema().ID)
	}
	var document struct {
		Properties map[string]struct {
			Const any `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(bootstrapRaw, &document); err != nil {
		t.Fatal(err)
	}
	if version, ok := document.Properties["version"].Const.(float64); !ok || int(version) != CurrentVersion {
		t.Fatalf("schema bootstrap version = %#v, Go = %d", document.Properties["version"].Const, CurrentVersion)
	}
}

func TestControlCapabilityValidationIsCanonicalAndRedacted(t *testing.T) {
	secret := "this-value-must-never-appear-in-an-error"
	if err := ValidateControlCapability(secret); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid capability error = %v", err)
	}
}

func TestRuntimeCapabilityValidationIsBoundedAndRedacted(t *testing.T) {
	valid := RuntimeCapabilities{ExecutorMCP: "executor-capability", LLMProxy: "llmproxy-capability"}
	if err := ValidateRuntimeCapabilities(valid); err != nil {
		t.Fatal(err)
	}
	secret := "this-runtime-secret-must-never-appear\n"
	valid.ExecutorMCP = secret
	if err := ValidateRuntimeCapabilities(valid); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid runtime capability error = %v", err)
	}
}

func bootstrapSchemaDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate harnessbootstrap package")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema")
}
