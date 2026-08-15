package productiondeploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestManagedSandboxActivationManifestJSONSchemaMatchesStrictParser(t *testing.T) {
	root := productionRepositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "api", "schema", "managed-sandbox-activation-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("managed sandbox activation manifest schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve managed sandbox activation manifest schema: %v", err)
	}
	manifest := map[string]any{
		"schemaVersion": 1,
		"profiles": []any{map[string]any{
			"region": "cn", "policyRevision": "lark-readonly-cn-v2",
			"policyEvidenceRef":  "artifact://policy/cn/v2",
			"networkReportPath":  "/absolute/cn-report.json",
			"networkEvidenceRef": "artifact://network/cn/v2",
		}},
	}
	if err := resolved.Validate(manifest); err != nil {
		t.Fatalf("activation schema rejected a valid manifest: %v", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseManagedSandboxActivationManifest(encoded); err != nil {
		t.Fatalf("strict parser rejected schema-valid manifest: %v", err)
	}
	manifest["future"] = true
	if err := resolved.Validate(manifest); err == nil {
		t.Fatal("activation schema accepted an unknown field")
	}
}

func TestManagedSandboxActivationManifestParserRejectsDuplicateRegionBeforeLoadingReports(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"profiles":[` +
		`{"region":"cn","policyRevision":"cn-v1","policyEvidenceRef":"artifact://policy/cn","networkReportPath":"/absolute/cn-1.json","networkEvidenceRef":"artifact://network/cn-1"},` +
		`{"region":"cn","policyRevision":"cn-v2","policyEvidenceRef":"artifact://policy/cn","networkReportPath":"/absolute/cn-2.json","networkEvidenceRef":"artifact://network/cn-2"}]}`)
	if _, err := parseManagedSandboxActivationManifest(raw); err == nil {
		t.Fatal("activation manifest parser accepted a duplicate region")
	}
}
