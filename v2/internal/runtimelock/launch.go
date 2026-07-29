package runtimelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	execServerCodexPathUnix    = "bin/codex"
	execServerBwrapPath        = "codex-resources/bwrap"
	execServerNoPathDirectory  = ".agentserver-no-path"
	execServerPackageMetadata  = "codex-package.json"
	execServerBwrapArtifactKey = "bwrap"
)

var execServerArguments = []string{"exec-server", "--listen", "stdio", "--strict-config"}

// ExecServerLaunchPlan is produced only after every executable declared for
// the selected platform has passed runtime-lock verification. Program is
// always absolute. Environment replaces, rather than extends, the caller's
// PATH so stock Codex cannot discover executables from an ambient host PATH.
//
// The plan assumes the verified bundle is immutable between verification and
// exec. Production agentx must pair it with the platform safe-open/execute
// boundary documented in ARCHITECTURE.md.
type ExecServerLaunchPlan struct {
	program     string
	arguments   []string
	runtimePath string
	runtime     VerifiedRuntime
}

func (p ExecServerLaunchPlan) Program() string {
	return p.program
}

func (p ExecServerLaunchPlan) Arguments() []string {
	return append([]string(nil), p.arguments...)
}

func (p ExecServerLaunchPlan) Runtime() VerifiedRuntime {
	return p.runtime
}

// Environment applies the runtime PATH to an already sanitized, explicit
// child environment. It does not sanitize credentials or other variables.
func (p ExecServerLaunchPlan) Environment(base []string) ([]string, error) {
	if p.program == "" || !filepath.IsAbs(p.program) || p.runtimePath == "" || !filepath.IsAbs(p.runtimePath) {
		return nil, errors.New("exec-server launch plan is not initialized")
	}
	if _, err := os.Lstat(p.runtimePath); err == nil {
		return nil, fmt.Errorf("reserved no-PATH location %q must not exist", p.runtimePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect reserved no-PATH location: %w", err)
	}
	environment := make([]string, 0, len(base)+1)
	seen := make(map[string]struct{}, len(base)+1)
	for _, entry := range base {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || strings.ContainsRune(name, '\x00') || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("invalid environment entry %q", entry)
		}
		canonicalName := strings.ToUpper(name)
		if canonicalName == "PATH" {
			continue
		}
		if _, duplicate := seen[canonicalName]; duplicate {
			return nil, fmt.Errorf("duplicate environment variable %q", name)
		}
		seen[canonicalName] = struct{}{}
		environment = append(environment, entry)
	}
	environment = append(environment, "PATH="+p.runtimePath)
	return environment, nil
}

// PrepareExecServerLaunch verifies the complete platform artifact set and
// constructs the only supported stock exec-server launch shape. The Phase 1
// bundle deliberately uses the legacy minimal layout: bin/codex plus, on
// Linux, codex-resources/bwrap. Omitting codex-package.json prevents stock
// Codex from prepending an unverified package codex-path directory.
func (m Manifest) PrepareExecServerLaunch(root, platform string) (ExecServerLaunchPlan, error) {
	verified, err := m.VerifyPlatform(root, platform)
	if err != nil {
		return ExecServerLaunchPlan{}, err
	}
	if err := validateExecServerBundle(verified); err != nil {
		return ExecServerLaunchPlan{}, err
	}

	runtimePath := filepath.Join(verified.Root, execServerNoPathDirectory)
	if _, err := os.Lstat(runtimePath); err == nil {
		return ExecServerLaunchPlan{}, fmt.Errorf("reserved no-PATH location %q must not exist", runtimePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ExecServerLaunchPlan{}, fmt.Errorf("inspect reserved no-PATH location: %w", err)
	}

	return ExecServerLaunchPlan{
		program:     verified.Codex.Path,
		arguments:   append([]string(nil), execServerArguments...),
		runtimePath: runtimePath,
		runtime:     verified,
	}, nil
}

// VerifyAndStartExecServer keeps verification and process creation behind one
// call boundary. The starter is never invoked when any manifest, executable,
// layout, or controlled-PATH check fails.
func (m Manifest) VerifyAndStartExecServer(root, platform string, starter func(ExecServerLaunchPlan) error) error {
	if starter == nil {
		return errors.New("exec-server starter is required")
	}
	plan, err := m.PrepareExecServerLaunch(root, platform)
	if err != nil {
		return err
	}
	if err := starter(plan); err != nil {
		return fmt.Errorf("start verified exec-server: %w", err)
	}
	return nil
}

func validateExecServerBundle(runtime VerifiedRuntime) error {
	osName, _, found := strings.Cut(runtime.Platform, "-")
	if !found {
		return fmt.Errorf("invalid runtime platform %q", runtime.Platform)
	}
	if osName != "linux" && osName != "darwin" {
		return fmt.Errorf("stock exec-server launch profile is not characterized for %q", runtime.Platform)
	}

	wantCodex := filepath.Join(runtime.Root, filepath.FromSlash(execServerCodexPathUnix))
	if runtime.Codex.Path != wantCodex {
		return fmt.Errorf("exec-server Codex path = %q, want minimal-bundle path %q", runtime.Codex.Path, wantCodex)
	}

	metadataPath := filepath.Join(runtime.Root, execServerPackageMetadata)
	if _, err := os.Lstat(metadataPath); err == nil {
		return fmt.Errorf("minimal exec-server bundle must not contain %s", execServerPackageMetadata)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect exec-server package metadata: %w", err)
	}

	if osName == "linux" {
		bwrap, exists := runtime.ExternalExecutables[execServerBwrapArtifactKey]
		if !exists {
			return errors.New("linux exec-server bundle requires external executable \"bwrap\"")
		}
		wantBwrap := filepath.Join(runtime.Root, filepath.FromSlash(execServerBwrapPath))
		if bwrap.Path != wantBwrap {
			return fmt.Errorf("linux bwrap path = %q, want bundled-resource path %q", bwrap.Path, wantBwrap)
		}
	}
	return nil
}
