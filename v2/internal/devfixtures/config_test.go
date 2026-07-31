package devfixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestInsecureDevelopmentFixturesJSONSchemaContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate devfixtures package")
	}
	schemaPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "insecure-dev-fixtures.schema.json")
	rawSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	document := ConfigDocument{
		Version: CurrentConfigVersion,
		Authority: AuthorityDocument{
			WorkspaceID: "40000000-0000-4000-8000-000000000004",
			SessionID:   "50000000-0000-4000-8000-000000000005",
			ActorID:     "10000000-0000-4000-8000-000000000001",
		},
		Hydra: HydraDocument{
			IntrospectionEndpoint:  "http://127.0.0.1:17447/oauth2/introspect",
			BrowserBearerTokenFile: "/private/dev/secrets/browser-bearer.token",
			Audience:               BrowserTokenAudience, Scope: BrowserTokenScope, ResponseTTL: "15m",
		},
		LLMProxy: LLMProxyDocument{
			Endpoint:        "https://127.0.0.1:17448/v1",
			CertificateFile: "/private/dev/pki/llmproxy.crt", PrivateKeyFile: "/private/dev/pki/llmproxy.key",
			RunCapabilityKeyFile: "/private/dev/secrets/run-capability.key",
			Model:                "gpt-5", Provider: "llmproxy", ToolNamespace: ToolNamespace,
			ScriptedTool: ScriptedToolName, FinalMessage: "complete",
		},
	}
	rawDocument, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(rawDocument, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("valid fixture config rejected by schema: %v", err)
	}
	object := value.(map[string]any)
	object["future"] = true
	if err := resolved.Validate(object); err == nil {
		t.Fatal("fixture schema accepted an unknown top-level field")
	}
}

func TestValidateFixtureEndpointsRequiresExplicitCanonicalLoopback(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  string
		scheme string
	}{
		{"missing port", "https://127.0.0.1/v1", "https"},
		{"zero port", "https://127.0.0.1:0/v1", "https"},
		{"broad host", "https://0.0.0.0:17448/v1", "https"},
		{"hostname", "https://example.test:17448/v1", "https"},
		{"encoded path", "https://127.0.0.1:17448/%76%31", "https"},
		{"query", "https://127.0.0.1:17448/v1?x=1", "https"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateEndpoint("test", test.value, test.scheme); err == nil {
				t.Fatalf("unsafe endpoint %q was accepted", test.value)
			}
		})
	}
	if parsed, listen, err := validateEndpoint("test", "https://127.0.0.1:17448/v1", "https"); err != nil || parsed.Path != "/v1" || listen != "127.0.0.1:17448" {
		t.Fatalf("valid endpoint = %v %q %v", parsed, listen, err)
	}
}
