package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

func TestRunRenderUsesExactClosedArgumentsAndReportsChecksums(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := []string{}
	bundle := productiondeploy.Bundle{Files: []productiondeploy.RenderedFile{{Name: "file.json", SHA256: strings.Repeat("a", 64), Content: []byte("x")}}}
	exitCode := run(
		[]string{"render", "--output=/absolute/output", "--config=/absolute/config.json"},
		&stdout, &stderr,
		deployCommands{
			load: func(path string) (productiondeploy.LoadedConfig, error) {
				called = append(called, "load:"+path)
				return productiondeploy.LoadedConfig{}, nil
			},
			render: func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error) {
				called = append(called, "render")
				return bundle, nil
			},
			write: func(actual productiondeploy.Bundle, path string) error {
				called = append(called, "write:"+path)
				if len(actual.Files) != 1 {
					t.Fatal("writer received wrong bundle")
				}
				return nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 || strings.Join(called, ",") != "load:/absolute/config.json,render,write:/absolute/output" ||
		!strings.Contains(stdout.String(), strings.Repeat("a", 64)+"  file.json") {
		t.Fatalf("run = %d, calls %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunPreparePolicyBootstrapUsesExactClosedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := ""
	exitCode := run(
		[]string{"prepare-policy-bootstrap", "--output=/absolute/bootstrap.json", "--config=/absolute/active.json"},
		&stdout, &stderr,
		deployCommands{preparePolicyBootstrap: func(config, output string) error {
			called = config + "|" + output
			return nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 || called != "/absolute/active.json|/absolute/bootstrap.json" ||
		!strings.Contains(stdout.String(), "fail-closed bootstrap") {
		t.Fatalf("run = %d, called %q, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunPinTerminalRevisionUsesExactClosedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := ""
	exitCode := run(
		[]string{
			"pin-terminal-revision", "--revision-id=revision-v8", "--sandbox-id=sandbox-1",
			"--output=/absolute/pinned.json", "--config=/absolute/bootstrap.json",
		},
		&stdout, &stderr,
		deployCommands{pinManagedTerminal: func(config, output, sandboxID, revisionID string) error {
			called = strings.Join([]string{config, output, sandboxID, revisionID}, "|")
			return nil
		}},
	)
	want := "/absolute/bootstrap.json|/absolute/pinned.json|sandbox-1|revision-v8"
	if exitCode != 0 || stderr.Len() != 0 || called != want ||
		!strings.Contains(stdout.String(), "fail-closed Terminal revision") {
		t.Fatalf("run = %d, called %q, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunRetargetTerminalSandboxUsesExactClosedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := ""
	digest := strings.Repeat("d", 64)
	exitCode := run(
		[]string{
			"retarget-terminal-sandbox", "--revision-id=revision-v9", "--sandbox-id=sandbox-new",
			"--expected-sandbox-id=sandbox-old", "--output=/absolute/retargeted.json",
			"--config=/absolute/bootstrap.json", "--managed-sandbox-image=sandbox@sha256:" + digest,
		},
		&stdout, &stderr,
		deployCommands{retargetManagedTerminal: func(config, output, expected, sandbox, revision, image string) error {
			called = strings.Join([]string{config, output, expected, sandbox, revision, image}, "|")
			return nil
		}},
	)
	want := strings.Join([]string{
		"/absolute/bootstrap.json", "/absolute/retargeted.json", "sandbox-old", "sandbox-new", "revision-v9",
		"sandbox@sha256:" + digest,
	}, "|")
	if exitCode != 0 || stderr.Len() != 0 || called != want ||
		!strings.Contains(stdout.String(), "fail-closed Terminal Sandbox") {
		t.Fatalf("run = %d, called %q, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunActivateManagedExecutorUsesExactEvidenceArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := ""
	exitCode := run([]string{
		"activate-managed-executor", "--config=/absolute/bootstrap.json", "--output=/absolute/active.json",
		"--network-report=/absolute/sg-network-report.json",
		"--policy-revision=lark-v2", "--policy-evidence-ref=ticket/123",
		"--network-evidence-ref=artifact/report.json",
	}, &stdout, &stderr, deployCommands{
		activateManagedExecutor: func(input, output, report, revision, policyRef, networkRef string) error {
			called = strings.Join([]string{input, output, report, revision, policyRef, networkRef}, "|")
			return nil
		},
	})
	want := strings.Join([]string{
		"/absolute/bootstrap.json", "/absolute/active.json", "/absolute/sg-network-report.json",
		"lark-v2", "ticket/123", "artifact/report.json",
	}, "|")
	if exitCode != 0 || stderr.Len() != 0 || called != want || !strings.Contains(stdout.String(), "evidence-bound active") {
		t.Fatalf("run = %d, called %q, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunLockReleasePassesExactAuthorityAndWritesOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := []string{}
	wantRaw := []byte("locked\n")
	digest := strings.Repeat("a", 64)
	arguments := []string{
		"lock-release", "--config=/absolute/template.json", "--output=/absolute/production.json",
		"--service-image=service@sha256:" + digest, "--harness-image=harness@sha256:" + digest,
		"--hydra-image=hydra@sha256:" + digest, "--managed-sandbox-image=sandbox@sha256:" + digest,
		"--lark-cli-sha256=" + digest, "--lark-skill-sha256=" + digest,
	}
	exitCode := run(arguments, &stdout, &stderr, deployCommands{
		load: func(path string) (productiondeploy.LoadedConfig, error) {
			called = append(called, "load:"+path)
			return productiondeploy.LoadedConfig{}, nil
		},
		lock: func(_ productiondeploy.LoadedConfig, lock productiondeploy.ReleaseLock) ([]byte, error) {
			called = append(called, strings.Join([]string{
				lock.ServiceImage, lock.HarnessImage, lock.HydraImage, lock.ManagedSandboxImage,
				lock.LarkCLISHA256, lock.LarkSkillSHA256,
			}, ","))
			return wantRaw, nil
		},
		writeLock: func(raw []byte, path string) error {
			if !bytes.Equal(raw, wantRaw) {
				t.Fatalf("locked bytes = %q", raw)
			}
			called = append(called, "write:"+path)
			return nil
		},
	})
	if exitCode != 0 || stderr.Len() != 0 || len(called) != 3 ||
		called[0] != "load:/absolute/template.json" || called[2] != "write:/absolute/production.json" ||
		!strings.Contains(stdout.String(), "/absolute/production.json") {
		t.Fatalf("run = %d, calls %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunLockDeveloperServicePassesOnlyServiceAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := []string{}
	wantRaw := []byte("locked\n")
	digest := strings.Repeat("a", 64)
	exitCode := run([]string{
		"lock-developer-service", "--config=/absolute/active.json", "--output=/absolute/production.json",
		"--service-image=registry/service@sha256:" + digest,
	}, &stdout, &stderr, deployCommands{
		load: func(path string) (productiondeploy.LoadedConfig, error) {
			called = append(called, "load:"+path)
			return productiondeploy.LoadedConfig{}, nil
		},
		lockDeveloperService: func(_ productiondeploy.LoadedConfig, image string) ([]byte, error) {
			called = append(called, "lock:"+image)
			return wantRaw, nil
		},
		writeLock: func(raw []byte, path string) error {
			if !bytes.Equal(raw, wantRaw) {
				t.Fatalf("locked bytes = %q", raw)
			}
			called = append(called, "write:"+path)
			return nil
		},
	})
	if exitCode != 0 || stderr.Len() != 0 || strings.Join(called, ",") !=
		"load:/absolute/active.json,lock:registry/service@sha256:"+digest+",write:/absolute/production.json" ||
		!strings.Contains(stdout.String(), "service-only development config") {
		t.Fatalf("run = %d, calls %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunRejectsDuplicateUnknownAndMissingArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{}, {"render"},
		{"render", "--config=/a", "--config=/b"},
		{"render", "--config=/a", "--future=/b"},
		{"validate", "config=/a"},
	} {
		if exitCode := run(arguments, &bytes.Buffer{}, &bytes.Buffer{}, deployCommands{}); exitCode != 2 {
			t.Fatalf("arguments %v exit = %d", arguments, exitCode)
		}
	}
}

func TestRunStopsBeforeWriteOnRenderFailure(t *testing.T) {
	exitCode := run(
		[]string{"render", "--config=/absolute/config", "--output=/absolute/output"},
		&bytes.Buffer{}, &bytes.Buffer{},
		deployCommands{
			load: func(string) (productiondeploy.LoadedConfig, error) { return productiondeploy.LoadedConfig{}, nil },
			render: func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error) {
				return productiondeploy.Bundle{}, errors.New("render failed")
			},
			write: func(productiondeploy.Bundle, string) error {
				t.Fatal("write called after render failure")
				return nil
			},
		},
	)
	if exitCode != 1 {
		t.Fatalf("render failure exit = %d", exitCode)
	}
}

func TestRunChartUsesExactClosedArgumentsAndReportsChecksums(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := []string{}
	chart := productiondeploy.HelmChart{Files: []productiondeploy.RenderedFile{{
		Name: "Chart.yaml", SHA256: strings.Repeat("b", 64), Content: []byte("chart"),
	}}}
	exitCode := run(
		[]string{"chart", "--output=/absolute/chart", "--config=/absolute/config.json"},
		&stdout, &stderr,
		deployCommands{
			load: func(path string) (productiondeploy.LoadedConfig, error) {
				called = append(called, "load:"+path)
				return productiondeploy.LoadedConfig{}, nil
			},
			chart: func(productiondeploy.LoadedConfig) (productiondeploy.HelmChart, error) {
				called = append(called, "chart")
				return chart, nil
			},
			writeChart: func(actual productiondeploy.HelmChart, path string) error {
				called = append(called, "write:"+path)
				if len(actual.Files) != 1 {
					t.Fatal("writer received wrong chart")
				}
				return nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 || strings.Join(called, ",") != "load:/absolute/config.json,chart,write:/absolute/chart" ||
		!strings.Contains(stdout.String(), strings.Repeat("b", 64)+"  Chart.yaml") {
		t.Fatalf("run = %d, calls %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}
