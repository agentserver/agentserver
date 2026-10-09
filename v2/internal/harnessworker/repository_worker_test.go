package harnessworker

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/workspacecontext"
)

func TestRepositoryContextReachesModelBeforeAnyTool(t *testing.T) {
	for _, available := range []bool{false, true} {
		f := newOneShotWorkerFixture(t)
		f.manifest.Workspace = &runmanifest.WorkspaceAuthority{EnvironmentID: "50000000-0000-4000-8000-000000000005", EnvironmentVersion: 1, RootSHA256: strings.Repeat("1", 64), WorkingDirectory: "src", WorkingDirectoryVersion: 2, RepositoryID: f.manifest.SessionID}
		f.manifest.ManagedSandbox = &runmanifest.ManagedSandboxAuthority{SettingVersion: 1, Region: "sg", EnvironmentID: f.manifest.Workspace.EnvironmentID}
		f.config.BaseInstructions = "Managed project test"
		digest := sha256.Sum256([]byte(f.config.BaseInstructions))
		f.manifest.ToolPack = &runmanifest.ToolPackAuthority{PackID: "managed-cli@v1", SkillSHA256: base64ToHex(digest[:])}
		seed := sha256.Sum256([]byte("one-shot-worker-signing-key"))
		signed, err := runmanifest.Sign(f.manifest, "one-shot-worker-key", ed25519.NewKeyFromSeed(seed[:]))
		if err != nil {
			t.Fatal(err)
		}
		bootstrap, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{Version: harnessbootstrap.CurrentVersion, SignedManifest: signed, ControlCapability: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), RuntimeCapabilities: harnessbootstrap.RuntimeCapabilities{ExecutorMCP: oneShotExecutorCapability, LLMProxy: oneShotLLMCapability}})
		if err != nil {
			t.Fatal(err)
		}
		f.config.BootstrapPipe = pipeWithWorkerBytes(t, bootstrap)
		if available {
			f.mcp.projectContext, err = workspacecontext.Encode(f.manifest.SessionID, workspacecontext.Snapshot{Version: 1, WorkingDirectory: "src", Instructions: []workspacecontext.Instruction{{Path: "AGENTS.md", Text: "Use the project test runner"}}, Skills: []workspacecontext.Skill{{Name: "check", Description: "Run project checks", Path: ".agents/skills/check/SKILL.md"}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		err = runOneShotWorker(t.Context(), f.config, f.dependencies())
		if !available {
			if err == nil || f.processConfig.Environment.ModelCapability != "" {
				t.Fatal("model started without repository preflight")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.runner.request.UserText, "Use the project test runner") || !strings.Contains(f.runner.request.UserText, ".agents/skills/check/SKILL.md") || !strings.HasSuffix(f.runner.request.UserText, oneShotPrompt) {
			t.Fatal("project context or original prompt lost")
		}
		if strings.Contains(f.runner.request.UserText, oneShotExecutorCapability) {
			t.Fatal("capability exposed to model")
		}
	}
}
