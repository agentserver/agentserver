package harnessworker

import (
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestWorkspaceDeveloperInstructionsDescribeExecutorWorkspaceAndSkills(t *testing.T) {
	catalog, err := BuildCatalog("executor", "executor tools", []ToolDescriptor{
		{Name: "list_environments", Description: "list", InputSchema: map[string]any{"type": "object"}},
		{Name: "read_file", Description: "read", InputSchema: map[string]any{"type": "object"}},
		{Name: "shell", Description: "shell", InputSchema: map[string]any{"type": "object"}},
	}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	manifest := runmanifest.Manifest{
		Workspace: &runmanifest.WorkspaceAuthority{
			EnvironmentID:    "10000000-0000-4000-8000-000000000001",
			WorkingDirectory: "customer-project",
		},
	}
	instructions := workspaceDeveloperInstructions(manifest, catalog)
	for _, want := range []string{
		"executor.list_environments",
		"executor.shell",
		"executor.read_file",
		"skills, .agents/skills, .codex/skills, .dsh/skills",
		"SKILL.md",
		"permission mode",
		"next run",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("workspace instructions do not contain %q:\n%s", want, instructions)
		}
	}
	for _, forbidden := range []string{
		"10000000-0000-4000-8000-000000000001",
		"customer-project", // the authority value must not be interpolated as a path
		"/Users/",
		"/workspace",
	} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("workspace instructions unexpectedly contain %q", forbidden)
		}
	}
	if len(instructions) > MaximumPromptBytes {
		t.Fatalf("workspace instructions length = %d, exceeds prompt limit", len(instructions))
	}
}

func TestWorkspaceDeveloperInstructionsAdaptToCatalog(t *testing.T) {
	manifest := runmanifest.Manifest{Workspace: &runmanifest.WorkspaceAuthority{}}
	catalog, err := BuildCatalog("executor", "executor tools", []ToolDescriptor{{
		Name: "read_file", Description: "read", InputSchema: map[string]any{"type": "object"},
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	instructions := workspaceDeveloperInstructions(manifest, catalog)
	if !strings.Contains(instructions, "executor.read_file") || strings.Contains(instructions, "executor.shell for workspace commands") {
		t.Fatalf("catalog-specific workspace instructions = %s", instructions)
	}
}

func TestWorkspaceDeveloperInstructionsOmittedWithoutWorkspace(t *testing.T) {
	if got := workspaceDeveloperInstructions(runmanifest.Manifest{}, nil); got != "" {
		t.Fatalf("instructions without workspace = %q, want empty", got)
	}
}

func TestAppServerRequestCarriesWorkspaceInstructionsOnFreshAndResume(t *testing.T) {
	catalog, err := BuildCatalog("executor", "executor tools", []ToolDescriptor{{
		Name: "list_environments", Description: "list", InputSchema: map[string]any{"type": "object"},
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	manifest := runmanifest.Manifest{
		RunID: "10000000-0000-4000-8000-000000000001",
		Model: runmanifest.ModelRoute{Model: "gpt-5"},
		Workspace: &runmanifest.WorkspaceAuthority{
			EnvironmentID: "20000000-0000-4000-8000-000000000002", EnvironmentVersion: 1,
			RootSHA256: strings.Repeat("a", 64), WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 1,
		},
	}
	runtime := PreparedAppServerRuntime{ThreadCWD: "/empty-workspace", RolloutPath: "/empty-workspace/rollout.jsonl"}
	fresh := appServerRequest(manifest, "hello", "base", catalog, runtime, nil, AppServerClientInfo{Name: "test", Title: "test", Version: "1"})
	if fresh.Start == nil || fresh.Start.DeveloperInstructions == "" || fresh.Resume != nil {
		t.Fatalf("fresh workspace app-server request = %+v", fresh)
	}
	resumed := appServerRequest(manifest, "hello", "base", catalog, runtime, &RestoredCheckpoint{
		Manifest: checkpoint.Manifest{BrainThreadID: "thread-1", CatalogDigest: catalog.Digest()},
	}, AppServerClientInfo{Name: "test", Title: "test", Version: "1"})
	if resumed.Resume == nil || resumed.Resume.DeveloperInstructions == "" || resumed.Start != nil {
		t.Fatalf("resumed workspace app-server request = %+v", resumed)
	}
}
