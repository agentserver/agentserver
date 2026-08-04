package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/harnesslayout"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	CodexConfigProfileStable0146 = "stable-0.146.0-dynamic-only-v1"

	maximumCodexConfigBytes = 64 * 1024
)

type LocalWorkerRuntimePreparerConfig struct {
	AttemptRoot            string
	RuntimeManifest        runtimelock.Manifest
	RuntimeManifestSHA256  string
	VerifiedRuntime        runtimelock.VerifiedRuntime
	FinalExec              runtimelock.VerifiedFile
	CodexConfigProfile     string
	TLSRootCertificateFile string
	WorkerUID              uint32
	WorkerGID              uint32
	AppUID                 uint32
	AppGID                 uint32
	MaxFrameBytes          int
	IncomingFrames         int
	MaxStderrBytes         int
}

// LocalWorkerRuntimePreparer materializes the fixed process-backend layout
// beneath the pool-created attempt root. It has no endpoint, prompt, command,
// or capability override surface.
type LocalWorkerRuntimePreparer struct {
	config LocalWorkerRuntimePreparerConfig
	mu     sync.Mutex
	used   bool
}

func NewLocalWorkerRuntimePreparer(config LocalWorkerRuntimePreparerConfig) (*LocalWorkerRuntimePreparer, error) {
	if err := validateLocalWorkerRuntimePreparerConfig(config); err != nil {
		return nil, err
	}
	return &LocalWorkerRuntimePreparer{config: config}, nil
}

func (preparer *LocalWorkerRuntimePreparer) Prepare(ctx context.Context, manifest runmanifest.Manifest) (PreparedWorkerRuntime, error) {
	if preparer == nil {
		return nil, errors.New("local worker runtime preparer is required")
	}
	if ctx == nil {
		return nil, errors.New("local worker runtime context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	preparer.mu.Lock()
	if preparer.used {
		preparer.mu.Unlock()
		return nil, errors.New("local worker runtime preparer is one-shot")
	}
	preparer.used = true
	preparer.mu.Unlock()
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if manifest.CodexRuntimeManifestDigest != preparer.config.RuntimeManifestSHA256 {
		return nil, errors.New("signed run manifest selects a different Codex runtime manifest")
	}
	if manifest.CheckpointAllowlistVersion != preparer.config.RuntimeManifest.CheckpointAllowlistVersion {
		return nil, errors.New("signed run manifest selects a different checkpoint allowlist version")
	}
	if err := validateLocalWorkerIdentity(
		preparer.config.WorkerUID, preparer.config.WorkerGID,
		preparer.config.AppUID, preparer.config.AppGID,
	); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(preparer.config.AttemptRoot)
	if err != nil {
		return nil, fmt.Errorf("read local attempt root: %w", err)
	}
	if len(entries) != 0 {
		return nil, errors.New("local attempt root must be empty before worker runtime preparation")
	}
	workerRoot := filepath.Join(preparer.config.AttemptRoot, harnesslayout.WorkerRuntimeDirectory)
	appRoot := filepath.Join(preparer.config.AttemptRoot, harnesslayout.AppRuntimeDirectory)
	if err := os.Mkdir(workerRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create local worker staging root: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(workerRoot)
			_ = os.Remove(appRoot)
		}
	}()
	if err := os.Mkdir(appRoot, 0o733); err != nil {
		return nil, fmt.Errorf("create local app runtime anchor: %w", err)
	}
	if err := os.Chmod(appRoot, 0o733); err != nil {
		return nil, fmt.Errorf("open local app runtime anchor for fixed app identity: %w", err)
	}
	restoreHome := filepath.Join(workerRoot, "checkpoint-home")
	stagingRoot := filepath.Join(workerRoot, "checkpoint-staging")
	for label, path := range map[string]string{
		"checkpoint restore home":   restoreHome,
		"checkpoint object staging": stagingRoot,
	} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return nil, fmt.Errorf("create local worker %s: %w", label, err)
		}
	}
	cleanup = false
	return &localPreparedWorkerRuntime{
		config: preparer.config, workerRoot: workerRoot, appRoot: appRoot,
		restoreHome: restoreHome, stagingRoot: stagingRoot,
	}, nil
}

type localPreparedWorkerRuntime struct {
	config      LocalWorkerRuntimePreparerConfig
	workerRoot  string
	appRoot     string
	restoreHome string
	stagingRoot string

	mu        sync.Mutex
	finalized bool
	closed    bool
}

func (runtime *localPreparedWorkerRuntime) CheckpointRoots() (string, string) {
	if runtime == nil {
		return "", ""
	}
	return runtime.restoreHome, runtime.stagingRoot
}

func (runtime *localPreparedWorkerRuntime) Finalize(
	ctx context.Context,
	manifest runmanifest.Manifest,
	restored *RestoredCheckpoint,
) (PreparedAppServerRuntime, error) {
	if runtime == nil {
		return PreparedAppServerRuntime{}, errors.New("local prepared worker runtime is required")
	}
	if ctx == nil {
		return PreparedAppServerRuntime{}, errors.New("local worker runtime finalization context is required")
	}
	if err := ctx.Err(); err != nil {
		return PreparedAppServerRuntime{}, err
	}
	runtime.mu.Lock()
	if runtime.finalized || runtime.closed {
		runtime.mu.Unlock()
		return PreparedAppServerRuntime{}, errors.New("local worker runtime finalization is one-shot")
	}
	runtime.finalized = true
	runtime.mu.Unlock()
	if manifest.CodexRuntimeManifestDigest != runtime.config.RuntimeManifestSHA256 ||
		manifest.CheckpointAllowlistVersion != runtime.config.RuntimeManifest.CheckpointAllowlistVersion {
		return PreparedAppServerRuntime{}, errors.New("local worker runtime authority changed after preparation")
	}
	configBytes, err := renderCodexConfig(runtime.config.CodexConfigProfile, manifest)
	if err != nil {
		return PreparedAppServerRuntime{}, err
	}
	paths, rolloutPath, err := installLocalAppRuntime(ctx, runtime.appRoot, configBytes, restored, runtime.config.AppUID, runtime.config.AppGID)
	clear(configBytes)
	if err != nil {
		return PreparedAppServerRuntime{}, err
	}
	if err := os.RemoveAll(runtime.restoreHome); err != nil {
		return PreparedAppServerRuntime{}, fmt.Errorf("remove local checkpoint restore staging: %w", err)
	}
	if err := os.RemoveAll(runtime.stagingRoot); err != nil {
		return PreparedAppServerRuntime{}, fmt.Errorf("remove local checkpoint object staging: %w", err)
	}
	processConfig := AppServerProcessConfig{
		FinalExecExecutable: runtime.config.FinalExec.Path,
		CodexExecutable:     runtime.config.VerifiedRuntime.Codex.Path,
		Directory:           paths.CWD,
		Environment: AppServerRuntimeEnvironment{
			Home: paths.Home, CodexHome: paths.CodexHome, Temporary: paths.Temporary,
			TLSRootCertificateFile: runtime.config.TLSRootCertificateFile,
		},
		WorkerUID: runtime.config.WorkerUID, WorkerGID: runtime.config.WorkerGID,
		AppUID: runtime.config.AppUID, AppGID: runtime.config.AppGID,
		MaxFrameBytes: runtime.config.MaxFrameBytes, IncomingFrames: runtime.config.IncomingFrames,
		MaxStderrBytes: runtime.config.MaxStderrBytes,
	}
	if _, _, err := validateAppServerProcessConfig(withRuntimeValidationCapability(processConfig)); err != nil {
		return PreparedAppServerRuntime{}, fmt.Errorf("validate finalized local app-server runtime: %w", err)
	}
	return PreparedAppServerRuntime{ProcessConfig: processConfig, ThreadCWD: paths.CWD, RolloutPath: rolloutPath}, nil
}

func withRuntimeValidationCapability(config AppServerProcessConfig) AppServerProcessConfig {
	config.Environment.ModelCapability = "validation-only-not-a-runtime-secret"
	return config
}

func (runtime *localPreparedWorkerRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil
	}
	runtime.closed = true
	runtime.mu.Unlock()
	if err := os.RemoveAll(runtime.workerRoot); err != nil {
		return fmt.Errorf("remove local worker staging root: %w", err)
	}
	// appRoot is deliberately left in place. Its 0700 descendants belong to
	// the fixed app UID, so the unprivileged sealed worker cannot reliably
	// traverse them. The harness-pool reaps the full process group and then
	// removes the entire attempt with its verified production runtime cleaner.
	return nil
}

type localAppRuntimePaths struct {
	Home      string
	CodexHome string
	Temporary string
	CWD       string
}

func validateLocalWorkerRuntimePreparerConfig(config LocalWorkerRuntimePreparerConfig) error {
	if config.AttemptRoot == "" || !filepath.IsAbs(config.AttemptRoot) || filepath.Clean(config.AttemptRoot) != config.AttemptRoot {
		return errors.New("local worker attempt root must be an absolute clean path")
	}
	root, err := os.Lstat(config.AttemptRoot)
	if err != nil {
		return fmt.Errorf("inspect local worker attempt root: %w", err)
	}
	if !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || root.Mode().Perm()&0o006 != 0 || root.Mode().Perm()&0o001 == 0 {
		return fmt.Errorf("local worker attempt root must be a real directory with execute-only app traversal: mode=%s", root.Mode())
	}
	if err := config.RuntimeManifest.Validate(); err != nil {
		return fmt.Errorf("validate local Codex runtime manifest: %w", err)
	}
	if !isCanonicalLocalSHA256(config.RuntimeManifestSHA256) {
		return errors.New("local Codex runtime manifest digest must be canonical lowercase SHA-256")
	}
	if err := validateLocalVerifiedFile("stock Codex", config.VerifiedRuntime.Codex); err != nil {
		return err
	}
	if err := validateLocalVerifiedFile("harness-final-exec", config.FinalExec); err != nil {
		return err
	}
	if config.CodexConfigProfile != CodexConfigProfileStable0146 || config.RuntimeManifest.CodexRelease != "0.146.0" {
		return errors.New("local Codex config profile must exactly match stable stock Codex 0.146.0")
	}
	if config.TLSRootCertificateFile == "" || !filepath.IsAbs(config.TLSRootCertificateFile) ||
		filepath.Clean(config.TLSRootCertificateFile) != config.TLSRootCertificateFile ||
		strings.ContainsRune(config.TLSRootCertificateFile, 0) {
		return errors.New("local app-server TLS root certificate file must be an absolute clean path")
	}
	if err := validateAppServerIdentity("worker", config.WorkerUID, config.WorkerGID); err != nil {
		return err
	}
	if err := validateAppServerIdentity("app", config.AppUID, config.AppGID); err != nil {
		return err
	}
	if config.WorkerUID == config.AppUID || config.WorkerGID == config.AppGID {
		return errors.New("local worker and app identities must be distinct")
	}
	if err := validateAppServerProcessBounds(appServerProcessBounds{
		maxFrameBytes:  valueOrDefault(config.MaxFrameBytes, 1),
		incomingFrames: valueOrDefault(config.IncomingFrames, 1),
		maxStderrBytes: valueOrDefault(config.MaxStderrBytes, 1),
	}); err != nil {
		return err
	}
	return nil
}

func validateLocalVerifiedFile(label string, file runtimelock.VerifiedFile) error {
	if file.Path == "" || !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path ||
		!isCanonicalLocalSHA256(file.SHA256) || file.SizeBytes < 1 {
		return fmt.Errorf("local verified %s artifact is invalid", label)
	}
	return nil
}

func isCanonicalLocalSHA256(value string) bool {
	return len(value) == 64 && value == strings.ToLower(value) && equalDigest(value, value)
}

func valueOrDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func renderCodexConfig(profile string, manifest runmanifest.Manifest) ([]byte, error) {
	if profile != CodexConfigProfileStable0146 {
		return nil, errors.New("unsupported Codex config profile")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	model, err := tomlBasicString(manifest.Model.Model)
	if err != nil {
		return nil, err
	}
	provider, err := tomlBasicString(manifest.Model.Provider)
	if err != nil {
		return nil, err
	}
	endpoint, err := tomlBasicString(manifest.Model.Endpoint)
	if err != nil {
		return nil, err
	}
	config := fmt.Sprintf(`model = %s
approval_policy = "never"
approvals_reviewer = "user"
sandbox_mode = "read-only"
model_provider = %s
web_search = "disabled"

[model_providers.%s]
name = "agentserver v2 llmproxy"
base_url = %s
env_key = %q
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0

[tools.update_plan]
enabled = false

[tools.experimental_request_user_input]
enabled = false

[agents]
enabled = false

[orchestrator.skills]
enabled = false

[orchestrator.mcp]
enabled = false

[skills.bundled]
enabled = false

[features]
apps = false
browser_use = false
browser_use_external = false
browser_use_full_cdp_access = false
code_mode = false
code_mode_only = false
computer_use = false
default_mode_request_user_input = false
goals = false
hooks = false
image_generation = false
in_app_browser = false
multi_agent = false
multi_agent_v2 = false
plugins = false
request_permissions_tool = false
shell_tool = false
skill_mcp_dependency_install = false
skill_search = false
standalone_web_search = false
tool_suggest = false
unified_exec = false
workspace_dependencies = false
`, model, provider, provider, endpoint, AppServerModelCapabilityEnvironment)
	if len(config) > maximumCodexConfigBytes {
		return nil, fmt.Errorf("rendered Codex config exceeds %d bytes", maximumCodexConfigBytes)
	}
	if strings.Contains(config, "[mcp_servers") || strings.Contains(config, "executor-gateway") {
		return nil, errors.New("rendered Codex config crossed the executor MCP boundary")
	}
	return []byte(config), nil
}

func tomlBasicString(value string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode Codex TOML string: %w", err)
	}
	return string(raw), nil
}
