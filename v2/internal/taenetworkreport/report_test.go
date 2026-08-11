package taenetworkreport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestReportCanonicalRoundTripAndDigest(t *testing.T) {
	report := validReport()
	raw, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte{'\n'}) != 1 || raw[len(raw)-1] != '\n' || !bytes.HasPrefix(raw, []byte(`{"schemaVersion":3,"kind":"agentserver.tae.sg-network-report"`)) {
		t.Fatalf("report is not one canonical line: %q", raw)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Configuration.ProxyURL != report.Configuration.ProxyURL || len(SHA256(raw)) != 64 {
		t.Fatalf("parsed report = %+v digest=%q", parsed, SHA256(raw))
	}
	for name, changed := range map[string][]byte{
		"missing LF": raw[:len(raw)-1],
		"prefix":     append([]byte("sdk log\n"), raw...),
		"indent":     bytes.Replace(raw, []byte(`,"kind"`), []byte(",\n  \"kind\""), 1),
		"unknown":    bytes.Replace(raw, []byte(`,"checks"`), []byte(`,"secret":"forbidden","checks"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(changed); err == nil {
				t.Fatal("non-canonical report was accepted")
			}
		})
	}
}

func TestValidateRejectsInconsistentEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*Report){
		"false passed":    func(value *Report) { value.Passed = false },
		"failed mismatch": func(value *Report) { value.Checks[0].Failed = 1 },
		"missing cleanup": func(value *Report) { value.CleanupConfirmed = false },
		"duplicate check": func(value *Report) { value.Checks[1].Name = value.Checks[0].Name },
		"zero digest": func(value *Report) {
			value.Configuration.DeploymentConfigSHA256 = strings.Repeat("0", 64)
		},
		"mutable image": func(value *Report) { value.Configuration.SandboxImage = "sandbox:latest" },
		"invalid sandbox ID": func(value *Report) {
			value.Configuration.SandboxID = "Sandbox-1"
		},
		"missing sandbox revision ID": func(value *Report) {
			value.Configuration.SandboxRevisionID = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			report := validReport()
			mutate(&report)
			if err := Validate(report); err == nil {
				t.Fatal("invalid evidence was accepted")
			}
		})
	}
}

func TestLoadRequiresStableNonWritableCanonicalFile(t *testing.T) {
	raw, err := Marshal(validReport())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, loaded, err := Load(path); err != nil || !bytes.Equal(loaded, raw) {
		t.Fatalf("Load() error=%v bytes=%q", err, loaded)
	}
	if err := os.Chmod(path, 0o620); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("group-writable report was accepted")
	}
}

func TestReportJSONSchemaAcceptsCanonicalContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate taenetworkreport package")
	}
	rawSchema, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "tae-network-report.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("TAE network report schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve TAE network report schema: %v", err)
	}
	raw, err := Marshal(validReport())
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("schema rejected canonical report: %v", err)
	}
	value.(map[string]any)["future"] = true
	if err := resolved.Validate(value); err == nil {
		t.Fatal("schema accepted an unknown report field")
	}
}

func validReport() Report {
	started := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	return Report{
		SchemaVersion: CurrentVersion, Kind: Kind, StartedAt: started, FinishedAt: started.Add(time.Minute),
		Passed: true, CleanupConfirmed: true,
		Source: Source{
			Namespace: "agentserver", PodName: "tae-probe-abcde", PodUID: "12345678-1234-4234-8234-123456789abc",
			NodeName: "sg-node-1", ServiceAccount: "sandbox-gateway",
		},
		Configuration: Configuration{
			DeploymentConfigSHA256: strings.Repeat("1", 64), Region: "sg", PSM: "bytedance.sandbox.agentserver",
			PolicyRevision: "revision-1", ByteCloudSite: "i18n-tt", JWTEndpoint: "https://cloud-i18n-sg.bytedance.net",
			ProxyURL: "socks5h://proxy.example:1080", ControlPlaneHost: "controlplane.sg.example",
			DataPlaneDomainSuffix: "sg.example", SandboxImage: "registry.example/sandbox:sha256-" + strings.Repeat("2", 64),
			SandboxID: "sandbox-1", SandboxRevisionID: "revision-1",
			LarkCLIVersion: "1.0.69", LarkCLISHA256: strings.Repeat("3", 64), LarkSkillSHA256: strings.Repeat("4", 64),
			ConnectivityAttempts: 2, LifecycleAttempts: 1,
		},
		Checks: []Check{
			{Name: "jwt_force_refresh", Attempts: 2, Succeeded: 2, DurationsMillis: []int64{10, 11}},
			{Name: "control_cleanup", Attempts: 1, Succeeded: 1, DurationsMillis: []int64{12}},
		},
	}
}
