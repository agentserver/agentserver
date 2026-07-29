package runtimelock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareExecServerLaunchBuildsAbsoluteControlledPlan(t *testing.T) {
	root := t.TempDir()
	manifest := execServerTestManifest(t, root, "darwin-amd64", nil)

	plan, err := manifest.PrepareExecServerLaunch(root, "darwin-amd64")
	if err != nil {
		t.Fatalf("PrepareExecServerLaunch() error = %v", err)
	}
	if got, want := plan.Program(), filepath.Join(plan.Runtime().Root, "bin", "codex"); got != want {
		t.Fatalf("program = %q, want %q", got, want)
	}
	if !filepath.IsAbs(plan.Program()) {
		t.Fatalf("program is not absolute: %q", plan.Program())
	}
	if got, want := strings.Join(plan.Arguments(), " "), "exec-server --listen stdio --strict-config"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}

	environment, err := plan.Environment([]string{
		"HOME=/controlled/home",
		"PATH=/poison/one",
		"Path=/poison/two",
	})
	if err != nil {
		t.Fatalf("Environment() error = %v", err)
	}
	wantPath := "PATH=" + filepath.Join(plan.Runtime().Root, execServerNoPathDirectory)
	if got, want := strings.Join(environment, "\n"), "HOME=/controlled/home\n"+wantPath; got != want {
		t.Fatalf("environment = %q, want %q", got, want)
	}
	if _, err := os.Stat(strings.TrimPrefix(wantPath, "PATH=")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("controlled PATH location must stay nonexistent, stat error = %v", err)
	}
}

func TestPrepareExecServerLaunchRequiresBundledLinuxBwrap(t *testing.T) {
	root := t.TempDir()
	manifest := execServerTestManifest(t, root, "linux-amd64", map[string]string{
		"bwrap": execServerBwrapPath,
	})
	plan, err := manifest.PrepareExecServerLaunch(root, "linux-amd64")
	if err != nil {
		t.Fatalf("PrepareExecServerLaunch() error = %v", err)
	}
	if got, want := plan.Runtime().ExternalExecutables["bwrap"].Path, filepath.Join(plan.Runtime().Root, "codex-resources", "bwrap"); got != want {
		t.Fatalf("verified bwrap = %q, want %q", got, want)
	}

	missingRoot := t.TempDir()
	missing := execServerTestManifest(t, missingRoot, "linux-amd64", nil)
	started := false
	err = missing.VerifyAndStartExecServer(missingRoot, "linux-amd64", func(ExecServerLaunchPlan) error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "requires external executable \"bwrap\"") {
		t.Fatalf("missing-bwrap error = %v", err)
	}
	if started {
		t.Fatal("starter ran before the Linux bwrap requirement passed")
	}
}

func TestVerifyAndStartExecServerFailsBeforeStarter(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T) (Manifest, string, string)
		wantErr string
	}{
		{
			name: "codex digest mismatch",
			prepare: func(t *testing.T) (Manifest, string, string) {
				root := t.TempDir()
				manifest := execServerTestManifest(t, root, "darwin-amd64", nil)
				artifacts := manifest.Artifacts["darwin-amd64"]
				artifacts.Codex.SHA256 = strings.Repeat("0", 64)
				manifest.Artifacts["darwin-amd64"] = artifacts
				return manifest, root, "darwin-amd64"
			},
			wantErr: "SHA-256",
		},
		{
			name: "external executable digest mismatch",
			prepare: func(t *testing.T) (Manifest, string, string) {
				root := t.TempDir()
				manifest := execServerTestManifest(t, root, "linux-amd64", map[string]string{"bwrap": execServerBwrapPath})
				artifacts := manifest.Artifacts["linux-amd64"]
				bwrap := artifacts.ExternalExecutables["bwrap"]
				bwrap.SHA256 = strings.Repeat("0", 64)
				artifacts.ExternalExecutables["bwrap"] = bwrap
				manifest.Artifacts["linux-amd64"] = artifacts
				return manifest, root, "linux-amd64"
			},
			wantErr: "SHA-256",
		},
		{
			name: "stock package metadata would add PATH",
			prepare: func(t *testing.T) (Manifest, string, string) {
				root := t.TempDir()
				manifest := execServerTestManifest(t, root, "darwin-amd64", nil)
				writeFile(t, root, execServerPackageMetadata, "{}")
				return manifest, root, "darwin-amd64"
			},
			wantErr: "must not contain codex-package.json",
		},
		{
			name: "reserved no-PATH location exists",
			prepare: func(t *testing.T) (Manifest, string, string) {
				root := t.TempDir()
				manifest := execServerTestManifest(t, root, "darwin-amd64", nil)
				if err := os.Mkdir(filepath.Join(root, execServerNoPathDirectory), 0o700); err != nil {
					t.Fatal(err)
				}
				return manifest, root, "darwin-amd64"
			},
			wantErr: "must not exist",
		},
		{
			name: "uncharacterized platform",
			prepare: func(t *testing.T) (Manifest, string, string) {
				root := t.TempDir()
				manifest := execServerTestManifest(t, root, "windows-amd64", nil)
				return manifest, root, "windows-amd64"
			},
			wantErr: "not characterized",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, root, platform := test.prepare(t)
			started := false
			err := manifest.VerifyAndStartExecServer(root, platform, func(ExecServerLaunchPlan) error {
				started = true
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("VerifyAndStartExecServer() error = %v, want substring %q", err, test.wantErr)
			}
			if started {
				t.Fatal("starter ran despite failed runtime verification")
			}
		})
	}
}

func TestVerifiedExecServerLaunchDoesNotExecutePoisonedPATHCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script launch sentinel is Unix-specific")
	}
	root := t.TempDir()
	realMarker := filepath.Join(root, "real-started")
	poisonMarker := filepath.Join(root, "poison-started")
	realScript := "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" \"$PATH\" > \"$REAL_MARKER\"\n"
	manifest := execServerTestManifestWithCodex(t, root, "darwin-amd64", realScript, nil)

	poisonDirectory := filepath.Join(root, "poison")
	writeFile(t, poisonDirectory, "codex", "#!/bin/sh\nprintf poison > \"$POISON_MARKER\"\n")
	if err := os.Chmod(filepath.Join(poisonDirectory, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := manifest.VerifyAndStartExecServer(root, "darwin-amd64", func(plan ExecServerLaunchPlan) error {
		environment, err := plan.Environment([]string{
			"PATH=" + poisonDirectory,
			"REAL_MARKER=" + realMarker,
			"POISON_MARKER=" + poisonMarker,
		})
		if err != nil {
			return err
		}
		command := exec.Command(plan.Program(), plan.Arguments()...)
		command.Env = environment
		return command.Run()
	})
	if err != nil {
		t.Fatalf("VerifyAndStartExecServer() error = %v", err)
	}
	started, err := os.ReadFile(realMarker)
	if err != nil {
		t.Fatalf("verified Codex did not start: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(started), filepath.Join(canonicalRoot, "bin", "codex")) || !strings.Contains(string(started), "exec-server") {
		t.Fatalf("verified launch marker = %q", started)
	}
	if _, err := os.Stat(poisonMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PATH poison Codex was executed, stat error = %v", err)
	}
}

func TestExecServerLaunchEnvironmentRejectsDuplicateAndMalformedEntries(t *testing.T) {
	if _, err := (ExecServerLaunchPlan{}).Environment(nil); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("zero launch plan Environment() error = %v", err)
	}

	root := t.TempDir()
	manifest := execServerTestManifest(t, root, "darwin-amd64", nil)
	plan, err := manifest.PrepareExecServerLaunch(root, "darwin-amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range [][]string{
		{"HOME=/one", "home=/two"},
		{"MALFORMED"},
		{"=missing-name"},
		{"NUL=value\x00suffix"},
	} {
		if _, err := plan.Environment(environment); err == nil {
			t.Fatalf("Environment(%q) unexpectedly succeeded", environment)
		}
	}

	if err := os.Mkdir(filepath.Join(plan.Runtime().Root, execServerNoPathDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Environment(nil); err == nil || !strings.Contains(err.Error(), "must not exist") {
		t.Fatalf("created no-PATH location Environment() error = %v", err)
	}
}

func execServerTestManifest(t *testing.T, root, platform string, external map[string]string) Manifest {
	t.Helper()
	return execServerTestManifestWithCodex(t, root, platform, "stock-codex", external)
}

func execServerTestManifestWithCodex(t *testing.T, root, platform, codexContents string, external map[string]string) Manifest {
	t.Helper()
	codex := execServerTestArtifact(t, root, execServerCodexPathUnix, codexContents)
	executables := make(map[string]FileArtifact, len(external))
	for name, path := range external {
		executables[name] = execServerTestArtifact(t, root, path, "external-"+name)
	}
	manifest := validManifest()
	manifest.Artifacts = map[string]PlatformArtifacts{
		platform: {
			Codex:               codex,
			ExternalExecutables: executables,
		},
	}
	return manifest
}

func execServerTestArtifact(t *testing.T, root, relative, contents string) FileArtifact {
	t.Helper()
	writeFile(t, root, relative, contents)
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, size, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return FileArtifact{
		Path:      relative,
		SourceURL: "https://github.com/openai/codex/releases/download/rust-v0.145.0/runtime.tar.gz",
		SHA256:    digest,
		SizeBytes: size,
	}
}
