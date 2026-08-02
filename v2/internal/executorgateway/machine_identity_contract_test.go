package executorgateway

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
	"time"
)

func TestExecutorGatewayMachineIdentityOpenAPIContract(t *testing.T) {
	raw := readExecutorGatewayContract(t, "openapi", "executor-gateway.yaml")
	var document struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]struct {
			Post struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			} `json:"post"`
			Get struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
				Parameters  []struct {
					Name     string `json:"name"`
					In       string `json:"in"`
					Required bool   `json:"required"`
				} `json:"parameters"`
			} `json:"get"`
		} `json:"paths"`
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
			Schemas         map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
		Proof struct {
			Version          string   `json:"version"`
			Domain           string   `json:"domainUtf8WithTrailingNul"`
			Fields           []string `json:"fields"`
			GoldenSHA256     string   `json:"goldenTranscriptSha256"`
			SignatureProfile string   `json:"signature"`
		} `json:"x-agentserver-machine-proof"`
		Phase struct {
			GatewayReplicas              int  `json:"gatewayReplicas"`
			ChallengePersistence         bool `json:"challengePersistence"`
			CrossProcessResume           bool `json:"crossProcessResume"`
			MaximumChallengeTTLMillis    int  `json:"maximumChallengeTtlMs"`
			MaximumOutstandingChallenges int  `json:"maximumOutstandingChallenges"`
		} `json:"x-agentserver-phase1"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode executor-gateway OpenAPI: %v", err)
	}
	if document.OpenAPI != "3.1.0" || len(document.Paths) != 3 {
		t.Fatalf("executor-gateway OpenAPI identity/path count = %q/%d", document.OpenAPI, len(document.Paths))
	}
	wantPost := map[string]string{
		AgentxEnrollmentPath: "completeAgentxEnrollment",
		AgentxChallengePath:  "issueAgentxChallenge",
	}
	for path, operationID := range wantPost {
		operation, found := document.Paths[path]
		if !found || operation.Post.OperationID != operationID || len(operation.Post.Security) != 1 {
			t.Errorf("OpenAPI POST %s = %+v", path, operation.Post)
		}
	}
	connect, found := document.Paths[AgentxConnectPath]
	if !found || connect.Get.OperationID != "connectAgentxWebSocket" || len(connect.Get.Security) != 1 {
		t.Fatalf("OpenAPI connect operation = %+v", connect.Get)
	}
	var headers []string
	for _, parameter := range connect.Get.Parameters {
		if parameter.In != "header" || !parameter.Required {
			t.Errorf("connect parameter = %+v", parameter)
		}
		headers = append(headers, parameter.Name)
	}
	sort.Strings(headers)
	wantHeaders := []string{AgentxChallengeIDHeader, AgentxMachineProofHeader}
	sort.Strings(wantHeaders)
	if !slices.Equal(headers, wantHeaders) {
		t.Fatalf("connect proof headers = %q, want %q", headers, wantHeaders)
	}
	if len(document.Components.SecuritySchemes) != 2 || document.Components.SecuritySchemes["executorEnrollmentBearer"] == nil ||
		document.Components.SecuritySchemes["executorOAuthBearer"] == nil {
		t.Fatalf("executor-gateway security schemes = %v", document.Components.SecuritySchemes)
	}
	wantFields := jsonFieldNames(reflect.TypeFor[ExecutorChallengeResponse]())
	schema := document.Components.Schemas["ExecutorChallengeResponse"]
	sort.Strings(schema.Required)
	if !slices.Equal(schema.Required, wantFields) || !slices.Equal(sortedMapKeys(schema.Properties), wantFields) {
		t.Fatalf("challenge schema required/properties = %q/%q, want %q", schema.Required, sortedMapKeys(schema.Properties), wantFields)
	}
	for _, field := range []string{"issuedAt", "expiresAt"} {
		var timestamp struct {
			Precision string `json:"x-agentserver-timePrecision"`
		}
		if err := json.Unmarshal(schema.Properties[field], &timestamp); err != nil || timestamp.Precision != "whole-millisecond UTC" {
			t.Fatalf("challenge %s precision = %q, %v", field, timestamp.Precision, err)
		}
	}
	if document.Proof.Version != ExecutorWSSProofVersion || document.Proof.Domain != string(executorWSSProofDomain) ||
		len(document.Proof.Fields) != 10 || document.Proof.GoldenSHA256 != "6399fd9b16c2b8a75590083505bb08492a32d4a8b5548d9d74b560c344d484f4" ||
		document.Proof.SignatureProfile == "" {
		t.Fatalf("machine-proof extension = %+v", document.Proof)
	}
	if document.Phase.GatewayReplicas != 1 || document.Phase.ChallengePersistence || document.Phase.CrossProcessResume ||
		document.Phase.MaximumChallengeTTLMillis != int(MaximumExecutorChallengeTTL/time.Millisecond) ||
		document.Phase.MaximumOutstandingChallenges != maximumExecutorChallenges {
		t.Fatalf("phase-one executor identity extension = %+v", document.Phase)
	}
}

func TestAgentxAsyncAPIBindsProductionChallengeHeaders(t *testing.T) {
	raw := readExecutorGatewayContract(t, "asyncapi", "agentx-wss.yaml")
	var document struct {
		Servers map[string]struct {
			Pathname    string `json:"pathname"`
			Description string `json:"description"`
		} `json:"servers"`
		Phase struct {
			GatewayReplicas      int    `json:"gatewayReplicas"`
			ChallengePath        string `json:"challengePath"`
			ChallengeIDHeader    string `json:"challengeIdHeader"`
			MachineProofHeader   string `json:"machineProofHeader"`
			MachineProofVersion  string `json:"machineProofVersion"`
			ChallengePersistence bool   `json:"challengePersistence"`
			CrossProcessResume   bool   `json:"crossProcessResume"`
		} `json:"x-agentserver-phase1"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	server := document.Servers["executorGateway"]
	if server.Pathname != AgentxConnectPath || server.Description == "" || document.Phase.GatewayReplicas != 1 ||
		document.Phase.ChallengePath != AgentxChallengePath || document.Phase.ChallengeIDHeader != AgentxChallengeIDHeader ||
		document.Phase.MachineProofHeader != AgentxMachineProofHeader || document.Phase.MachineProofVersion != ExecutorWSSProofVersion ||
		document.Phase.ChallengePersistence || document.Phase.CrossProcessResume {
		t.Fatalf("agentx AsyncAPI machine proof = server %+v, phase %+v", server, document.Phase)
	}
}

func readExecutorGatewayContract(t *testing.T, kind, name string) []byte {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve executor-gateway contract source")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "api", kind, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func jsonFieldNames(value reflect.Type) []string {
	fields := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		name := value.Field(index).Tag.Get("json")
		if comma := strings.IndexByte(name, ','); comma >= 0 {
			name = name[:comma]
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
