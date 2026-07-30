package executorgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestEnvironmentResolverListsFrozenOnlineProjection(t *testing.T) {
	registry := &fakeEnvironmentRegistry{environments: []RegisteredEnvironment{
		testRegisteredEnvironment("60000000-0000-4000-8000-000000000007", `{"kind":"local","root":"/work/b","displayName":"B","description":"second","defaultCwd":"src"}`),
		testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/work/a"}`),
	}}
	registry.environments[0].ExecutorID = "20000000-0000-4000-8000-000000000003"
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Environments) != 2 || result.Environments[0].EnvironmentID != testEnvironmentID {
		t.Fatalf("environment list ordering = %+v", result.Environments)
	}
	if result.Environments[0].DisplayName != "environment "+testEnvironmentID || result.Environments[0].DefaultCWD != "." {
		t.Fatalf("default environment projection = %+v", result.Environments[0])
	}
	if result.Environments[1].DisplayName != "B" || result.Environments[1].DefaultCWD != "src" || result.Environments[1].Description != "second" {
		t.Fatalf("declared environment projection = %+v", result.Environments[1])
	}
}

func TestEnvironmentResolverRejectsUnsafeRootDescriptor(t *testing.T) {
	tests := []string{
		`{"kind":"local","root":"relative"}`,
		`{"kind":"local","root":"/work","defaultCwd":"../escape"}`,
		`{"kind":"local","root":"/work","future":true}`,
	}
	for _, descriptor := range tests {
		registry := &fakeEnvironmentRegistry{environments: []RegisteredEnvironment{testRegisteredEnvironment(testEnvironmentID, descriptor)}}
		resolver, err := NewEnvironmentResolver(registry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", ""); err == nil {
			t.Fatalf("unsafe root descriptor was accepted: %s", descriptor)
		}
	}
}

func TestEnvironmentResolverValidatesWindowsRootsLexically(t *testing.T) {
	valid := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"C:\\workspace"}`)
	valid.Platform = "windows-amd64"
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{valid}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", ""); err != nil {
		t.Fatalf("clean Windows root rejected: %v", err)
	}

	for _, descriptor := range []string{
		`{"kind":"local","root":"C:\\work\\..\\escape"}`,
		`{"kind":"local","root":"C:/work\\mixed"}`,
		`{"kind":"local","root":"C:/work/"}`,
	} {
		environment := testRegisteredEnvironment(testEnvironmentID, descriptor)
		environment.Platform = "windows-amd64"
		resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{environment}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", ""); err == nil {
			t.Fatalf("unsafe Windows root was accepted: %s", descriptor)
		}
	}
}

func TestEnvironmentResolverRejectsRegistryResponseOutsideExecutorFilter(t *testing.T) {
	registry := &fakeEnvironmentRegistry{
		environments:         []RegisteredEnvironment{testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace"}`)},
		ignoreExecutorFilter: true,
	}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", "20000000-0000-4000-8000-000000000099"); err == nil {
		t.Fatal("registry response outside requested executor filter was accepted")
	}
}

func TestEnvironmentResolverPreservesCompositeProfileAndRejectsUnknownProfile(t *testing.T) {
	environment := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace"}`)
	environment.OuterProfileVersion = execprofile.FilesystemReadVersion
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{environment}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(t.Context(), "40000000-0000-4000-8000-000000000004", testEnvironmentID)
	if err != nil {
		t.Fatalf("resolve composite profile: %v", err)
	}
	if !execprofile.SupportsFilesystemRead(resolved.OuterProfileVersion) {
		t.Fatalf("resolved profile = %q, want filesystem read", resolved.OuterProfileVersion)
	}

	environment.OuterProfileVersion = "filesystem-read-v1"
	resolver, err = NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{environment}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), "40000000-0000-4000-8000-000000000004", testEnvironmentID); err == nil || !strings.Contains(err.Error(), "outer profile") {
		t.Fatalf("unknown environment profile error = %v", err)
	}
}

func TestEnvironmentResolverBoundsAggregateProjection(t *testing.T) {
	environments := make([]RegisteredEnvironment, 256)
	for index := range environments {
		environmentID := fmt.Sprintf("60000000-0000-4000-8000-%012x", index+1)
		descriptor := fmt.Sprintf(`{"kind":"local","root":"/workspace","description":%q,"defaultCwd":%q}`,
			strings.Repeat("d", 2048), strings.Repeat("c", 4096))
		environments[index] = testRegisteredEnvironment(environmentID, descriptor)
	}
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: environments})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.List(t.Context(), "40000000-0000-4000-8000-000000000004", ""); err == nil || !strings.Contains(err.Error(), "projection") {
		t.Fatalf("oversized aggregate projection error = %v", err)
	}
}

type fakeEnvironmentRegistry struct {
	environments         []RegisteredEnvironment
	err                  error
	ignoreExecutorFilter bool
}

func (registry *fakeEnvironmentRegistry) ListEnvironments(_ context.Context, _, executorID string) ([]RegisteredEnvironment, error) {
	if registry.err != nil {
		return nil, registry.err
	}
	result := make([]RegisteredEnvironment, 0, len(registry.environments))
	for _, environment := range registry.environments {
		if !registry.ignoreExecutorFilter && executorID != "" && environment.ExecutorID != executorID {
			continue
		}
		copy := environment
		copy.RootDescriptor = append(json.RawMessage(nil), environment.RootDescriptor...)
		result = append(result, copy)
	}
	return result, nil
}

func testRegisteredEnvironment(environmentID, descriptor string) RegisteredEnvironment {
	return RegisteredEnvironment{
		EnvironmentID:        environmentID,
		ExecutorID:           testExecutorID,
		RootDescriptor:       json.RawMessage(descriptor),
		Platform:             "linux-arm64",
		OuterProfileVersion:  execprofile.Version,
		InsecureDev:          true,
		EnvironmentVersion:   1,
		ConnectionGeneration: 1,
	}
}
