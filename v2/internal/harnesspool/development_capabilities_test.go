package harnesspool

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestDevelopmentAttemptRuntimeCapabilitySourceIssuesBoundAudienceSeparatedTokens(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	codec := developmentCapabilityTestCodec(t)
	identities := []string{
		"81000000-0000-4000-8000-000000000001",
		"82000000-0000-4000-8000-000000000002",
	}
	config := DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig("83000000-0000-4000-8000-000000000003")
	config.ExpiryGrace = 30 * time.Second
	config.Now = func() time.Time { return now }
	config.IDGenerator = func() (string, error) {
		identity := identities[0]
		identities = identities[1:]
		return identity, nil
	}
	source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
	if err != nil {
		t.Fatal(err)
	}
	prepared := developmentCapabilityPreparedLaunch(t)
	capabilities, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := harnessbootstrap.ValidateRuntimeCapabilities(capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.ExecutorMCP == capabilities.LLMProxy {
		t.Fatal("executor and model runtime capabilities are identical")
	}

	executor, err := codec.Verify(capabilities.ExecutorMCP, runcapability.AudienceExecutorMCP, now)
	if err != nil {
		t.Fatal(err)
	}
	claim := prepared.Scheduled.Claim
	wantExpiry := now.Add(time.Duration(prepared.Manifest.Limits.MaxRunDurationMS)*time.Millisecond + config.ExpiryGrace).UnixMilli()
	wantRunDeadline := now.Add(time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond).UnixMilli()
	if executor.CapabilityID != "81000000-0000-4000-8000-000000000001" ||
		executor.WorkspaceID != prepared.Manifest.WorkspaceID || executor.SessionID != prepared.Manifest.SessionID ||
		executor.RunID != prepared.Manifest.RunID || executor.RunAttemptID != prepared.Manifest.RunAttemptID ||
		executor.RunAttemptGeneration != prepared.Manifest.RunAttemptGeneration || executor.ActorID != claim.Run.ActorID ||
		executor.HolderID != claim.RunAttempt.HolderID || executor.ExecutorID != config.ExecutorID ||
		executor.ToolCatalogDigest != prepared.Manifest.ExecutorMCP.CatalogDigest ||
		executor.ExpectedRunVersion != claim.Run.Version+1 || executor.ExpectedRunAttemptVersion != claim.RunAttempt.Version+1 ||
		executor.MaxApprovalTTLMillis != prepared.Manifest.Limits.MaxApprovalTTLMS ||
		executor.IssuedAtUnixMS != now.UnixMilli() || executor.RunDeadlineUnixMS != wantRunDeadline || executor.ExpiresAtUnixMS != wantExpiry ||
		executor.Model != "" || executor.Provider != "" {
		t.Fatalf("executor claims = %#v", executor)
	}

	model, err := codec.Verify(capabilities.LLMProxy, runcapability.AudienceLLMProxy, now)
	if err != nil {
		t.Fatal(err)
	}
	if model.CapabilityID != "82000000-0000-4000-8000-000000000002" ||
		model.WorkspaceID != executor.WorkspaceID || model.SessionID != executor.SessionID ||
		model.RunID != executor.RunID || model.RunAttemptID != executor.RunAttemptID ||
		model.RunAttemptGeneration != executor.RunAttemptGeneration || model.ActorID != executor.ActorID ||
		model.HolderID != executor.HolderID || model.IssuedAtUnixMS != executor.IssuedAtUnixMS ||
		model.RunDeadlineUnixMS != executor.RunDeadlineUnixMS ||
		model.ExpiresAtUnixMS != executor.ExpiresAtUnixMS || model.Model != prepared.Manifest.Model.Model ||
		model.Provider != prepared.Manifest.Model.Provider || model.ExecutorID != "" ||
		model.ToolCatalogDigest != "" || model.ExpectedRunVersion != 0 || model.ExpectedRunAttemptVersion != 0 || model.MaxApprovalTTLMillis != 0 {
		t.Fatalf("model claims = %#v", model)
	}
	if _, err := codec.Verify(capabilities.ExecutorMCP, runcapability.AudienceLLMProxy, now); err == nil {
		t.Fatal("executor capability crossed into the model audience")
	}
	if _, err := codec.Verify(capabilities.LLMProxy, runcapability.AudienceExecutorMCP, now); err == nil {
		t.Fatal("model capability crossed into the executor audience")
	}
}

func TestDevelopmentAttemptRuntimeCapabilitySourceCarriesWorkspaceAuthority(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	prepared := developmentCapabilityPreparedLaunch(t)
	rootDigest := sha256.Sum256([]byte(`{"kind":"local","root":"/workspace/projects"}`))
	prepared.Manifest.Workspace = &runmanifest.WorkspaceAuthority{
		EnvironmentID: "90000000-0000-4000-8000-000000000009", EnvironmentVersion: 2,
		RootSHA256: fmt.Sprintf("%x", rootDigest), WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 3,
	}
	seed := sha256.Sum256([]byte("launch-preparer-key"))
	signer, err := NewEd25519ManifestSigner("cluster-key-1", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	prepared.SignedManifest, err = signer.SignRunManifest(prepared.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Manifest.Workspace.Binding(); err != nil {
		t.Fatal(err)
	}
	config := DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig("83000000-0000-4000-8000-000000000003")
	config.Now = func() time.Time { return now }
	config.IDGenerator = developmentCapabilityIdentitySequence(
		"91000000-0000-4000-8000-000000000001", "91000000-0000-4000-8000-000000000002",
	)
	codec := developmentCapabilityTestCodec(t)
	source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := codec.Verify(capabilities.ExecutorMCP, runcapability.AudienceExecutorMCP, now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.WorkspaceEnvironmentID != prepared.Manifest.Workspace.EnvironmentID || claims.WorkspaceEnvironmentVersion != 2 ||
		claims.WorkspaceRootSHA256 != prepared.Manifest.Workspace.RootSHA256 || claims.WorkspaceWorkingDirectory != "rtm-aihub" || claims.WorkspaceWorkingDirectoryVersion != 3 {
		t.Fatalf("workspace capability claims = %+v", claims)
	}
}

func TestDevelopmentAttemptRuntimeCapabilitySourceRejectsInvalidAuthorityAndIdentity(t *testing.T) {
	prepared := developmentCapabilityPreparedLaunch(t)
	codec := developmentCapabilityTestCodec(t)
	baseConfig := DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig("83000000-0000-4000-8000-000000000003")
	baseConfig.Now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }

	t.Run("missing-actor", func(t *testing.T) {
		invalid := prepared
		invalid.Scheduled.Claim.Run.ActorID = ""
		config := baseConfig
		config.IDGenerator = developmentCapabilityIdentitySequence(
			"84000000-0000-4000-8000-000000000004",
			"85000000-0000-4000-8000-000000000005",
		)
		source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), invalid); err == nil {
			t.Fatal("missing actor authority was accepted")
		}
	})

	t.Run("duplicate-capability-id", func(t *testing.T) {
		config := baseConfig
		config.IDGenerator = func() (string, error) {
			return "86000000-0000-4000-8000-000000000006", nil
		}
		source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared); err == nil {
			t.Fatal("duplicate capability identities were accepted")
		}
	})

	t.Run("identity-error", func(t *testing.T) {
		identityErr := errors.New("synthetic random source failure")
		config := baseConfig
		config.IDGenerator = func() (string, error) { return "", identityErr }
		source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared); !errors.Is(err, identityErr) {
			t.Fatalf("identity failure = %v", err)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		config := baseConfig
		config.IDGenerator = developmentCapabilityIdentitySequence(
			"87000000-0000-4000-8000-000000000007",
			"88000000-0000-4000-8000-000000000008",
		)
		source, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.IssueAttemptRuntimeCapabilities(ctx, prepared); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled issuance = %v", err)
		}
	})
}

func TestDevelopmentAttemptRuntimeCapabilitySourceValidatesConfiguration(t *testing.T) {
	codec := developmentCapabilityTestCodec(t)
	valid := DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig("83000000-0000-4000-8000-000000000003")
	for name, mutation := range map[string]func(*DevelopmentAttemptRuntimeCapabilitySourceConfig){
		"executor": func(config *DevelopmentAttemptRuntimeCapabilitySourceConfig) { config.ExecutorID = "not-a-uuid" },
		"grace":    func(config *DevelopmentAttemptRuntimeCapabilitySourceConfig) { config.ExpiryGrace = 0 },
		"clock":    func(config *DevelopmentAttemptRuntimeCapabilitySourceConfig) { config.Now = nil },
		"identity": func(config *DevelopmentAttemptRuntimeCapabilitySourceConfig) { config.IDGenerator = nil },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutation(&config)
			if _, err := NewDevelopmentAttemptRuntimeCapabilitySource(codec, config); err == nil {
				t.Fatal("invalid development capability source configuration was accepted")
			}
		})
	}
	if _, err := NewDevelopmentAttemptRuntimeCapabilitySource(nil, valid); err == nil {
		t.Fatal("nil development capability codec was accepted")
	}
}

func developmentCapabilityPreparedLaunch(t *testing.T) PreparedRunLaunch {
	t.Helper()
	prepared := poolTestPreparedLaunch(t)
	prepared.Scheduled.Claim.Run.ActorID = "89000000-0000-4000-8000-000000000009"
	return prepared
}

func developmentCapabilityTestCodec(t *testing.T) *runcapability.DevelopmentCodec {
	t.Helper()
	codec, err := runcapability.NewDevelopmentCodec(bytes.Repeat([]byte{0x91}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func developmentCapabilityIdentitySequence(identities ...string) IDGenerator {
	return func() (string, error) {
		if len(identities) == 0 {
			return "", errors.New("development capability identity sequence exhausted")
		}
		identity := identities[0]
		identities = identities[1:]
		return identity, nil
	}
}
