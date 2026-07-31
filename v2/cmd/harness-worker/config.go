package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	workerDeploymentConfigVersion = 1
	maximumWorkerDeploymentBytes  = 64 * 1024
	maximumWorkerKeyringBytes     = 64 * 1024
	maximumWorkerRuntimeBytes     = 1024 * 1024
	maximumWorkerCABundleBytes    = 1024 * 1024
	maximumWorkerTLSIdentityBytes = 1024 * 1024
)

var workerDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type workerDeploymentDocument struct {
	Version                int                    `json:"version"`
	RunManifestKeyringFile string                 `json:"runManifestKeyringFile"`
	RuntimeManifestFile    string                 `json:"runtimeManifestFile"`
	RuntimeBundleRoot      string                 `json:"runtimeBundleRoot"`
	FinalExec              workerArtifactDocument `json:"finalExec"`
	CodexConfigProfile     string                 `json:"codexConfigProfile"`
	WorkerUID              uint32                 `json:"workerUid"`
	WorkerGID              uint32                 `json:"workerGid"`
	AppUID                 uint32                 `json:"appUid"`
	AppGID                 uint32                 `json:"appGid"`
	TLS                    workerTLSDocument      `json:"tls"`
}

type workerArtifactDocument struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type workerTLSDocument struct {
	CAFile          string `json:"caFile"`
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type loadedWorkerDeployment struct {
	keyring        *runmanifest.VerificationKeyring
	preparer       *harnessworker.LocalWorkerRuntimePreparer
	controlClient  *http.Client
	executorClient *http.Client
}

func parseWorkerArguments(arguments []string) (configPath string, checkpoint bool, ok bool) {
	if len(arguments) != 4 && len(arguments) != 5 {
		return "", false, false
	}
	if arguments[0] != "run" || arguments[2] != "--bootstrap-fd=3" || arguments[3] != "--prompt-fd=4" {
		return "", false, false
	}
	configPath, found := strings.CutPrefix(arguments[1], "--config=")
	if !found || configPath == "" || !filepath.IsAbs(configPath) || filepath.Clean(configPath) != configPath || strings.ContainsRune(configPath, 0) {
		return "", false, false
	}
	if len(arguments) == 5 {
		if arguments[4] != "--checkpoint-fd=5" {
			return "", false, false
		}
		checkpoint = true
	}
	return configPath, checkpoint, true
}

func executeWorker(ctx context.Context, configPath string, bootstrap, prompt, checkpoint *os.File) error {
	if ctx == nil {
		return errors.New("worker command context is required")
	}
	attemptRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("read worker attempt root: %w", err)
	}
	attemptRoot, err = filepath.Abs(attemptRoot)
	if err != nil {
		return fmt.Errorf("resolve worker attempt root: %w", err)
	}
	deployment, err := loadWorkerDeployment(configPath, filepath.Clean(attemptRoot))
	if err != nil {
		return err
	}
	defer deployment.controlClient.CloseIdleConnections()
	defer deployment.executorClient.CloseIdleConnections()
	return harnessworker.RunOneShotWorker(ctx, harnessworker.OneShotWorkerConfig{
		BootstrapPipe: bootstrap, PromptPipe: prompt, CheckpointPipe: checkpoint,
		VerificationKeyring: deployment.keyring, RuntimePreparer: deployment.preparer,
		ControlHTTPClient: deployment.controlClient, ExecutorHTTPClient: deployment.executorClient,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			// Approval transport is not part of harness-control 1.2 yet. Decline
			// deterministically; never infer approval in the worker.
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalDecline}, nil
		},
		ProgressHandler: func(context.Context, harnessworker.ProgressEvent) error { return nil },
		NotificationHandler: func(context.Context, codexwire.Message) error {
			// Runtime notifications and progress are forwarded to control by the
			// worker before these optional observers run. The production command
			// needs no second local consumer, but the runner must remain drained.
			return nil
		},
		ClientInfo: harnessworker.AppServerClientInfo{
			Name: "agentserver-harness-worker", Title: "Agentserver Harness Worker", Version: "v2",
		},
	})
}

func loadWorkerDeployment(configPath, attemptRoot string) (loadedWorkerDeployment, error) {
	raw, err := readBoundedWorkerFile("worker deployment config", configPath, maximumWorkerDeploymentBytes)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document workerDeploymentDocument
	if err := decoder.Decode(&document); err != nil {
		return loadedWorkerDeployment{}, fmt.Errorf("decode worker deployment config: %w", err)
	}
	if err := finishWorkerJSON(decoder); err != nil {
		return loadedWorkerDeployment{}, fmt.Errorf("finish worker deployment config: %w", err)
	}
	if err := validateWorkerDeploymentDocument(document); err != nil {
		return loadedWorkerDeployment{}, err
	}

	keyringBytes, err := readBoundedWorkerFile("run manifest keyring", document.RunManifestKeyringFile, maximumWorkerKeyringBytes)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	keyring, err := runmanifest.ParseVerificationKeyring(keyringBytes)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	runtimeBytes, err := readBoundedWorkerFile("Codex runtime manifest", document.RuntimeManifestFile, maximumWorkerRuntimeBytes)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	runtimeManifest, err := runtimelock.Parse(runtimeBytes)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	verifiedRuntime, err := runtimeManifest.VerifyCurrentPlatform(document.RuntimeBundleRoot)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	runtimeDigest := sha256.Sum256(runtimeBytes)
	finalExec, err := verifyWorkerArtifact("harness-final-exec", document.FinalExec)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	preparer, err := harnessworker.NewLocalWorkerRuntimePreparer(harnessworker.LocalWorkerRuntimePreparerConfig{
		AttemptRoot: attemptRoot, RuntimeManifest: runtimeManifest,
		RuntimeManifestSHA256: hex.EncodeToString(runtimeDigest[:]), VerifiedRuntime: verifiedRuntime,
		FinalExec: finalExec, CodexConfigProfile: document.CodexConfigProfile,
		WorkerUID: document.WorkerUID, WorkerGID: document.WorkerGID,
		AppUID: document.AppUID, AppGID: document.AppGID,
	})
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	controlClient, err := newWorkerHTTPClient(document.TLS)
	if err != nil {
		return loadedWorkerDeployment{}, err
	}
	executorClient, err := newWorkerHTTPClient(document.TLS)
	if err != nil {
		controlClient.CloseIdleConnections()
		return loadedWorkerDeployment{}, err
	}
	return loadedWorkerDeployment{
		keyring: keyring, preparer: preparer, controlClient: controlClient, executorClient: executorClient,
	}, nil
}

func validateWorkerDeploymentDocument(document workerDeploymentDocument) error {
	if document.Version != workerDeploymentConfigVersion {
		return fmt.Errorf("worker deployment config version must be %d", workerDeploymentConfigVersion)
	}
	for label, path := range map[string]string{
		"run manifest keyring": document.RunManifestKeyringFile,
		"runtime manifest":     document.RuntimeManifestFile,
		"runtime bundle root":  document.RuntimeBundleRoot,
		"final-exec artifact":  document.FinalExec.Path,
		"TLS CA":               document.TLS.CAFile,
		"TLS certificate":      document.TLS.CertificateFile,
		"TLS private key":      document.TLS.KeyFile,
	} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
			return fmt.Errorf("worker deployment %s path must be absolute and clean", label)
		}
	}
	if !workerDigestPattern.MatchString(document.FinalExec.SHA256) || document.FinalExec.SizeBytes < 1 {
		return errors.New("worker deployment final-exec artifact must have canonical SHA-256 and positive size")
	}
	if document.CodexConfigProfile != harnessworker.CodexConfigProfileStable0146 {
		return errors.New("worker deployment Codex config profile is unsupported")
	}
	for label, identity := range map[string]uint32{
		"worker UID": document.WorkerUID, "worker GID": document.WorkerGID,
		"app UID": document.AppUID, "app GID": document.AppGID,
	} {
		if identity == 0 || identity == ^uint32(0) {
			return fmt.Errorf("worker deployment %s must be an unprivileged identity", label)
		}
	}
	if document.WorkerUID == document.AppUID || document.WorkerGID == document.AppGID {
		return errors.New("worker deployment worker and app identities must be distinct")
	}
	return nil
}

func readBoundedWorkerFile(label, path string, maximum int64) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s path must be absolute", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 ||
		info.Size() < 1 || info.Size() > maximum {
		return nil, fmt.Errorf("%s must be a direct regular file between 1 and %d bytes: mode=%s size=%d", label, maximum, info.Mode(), info.Size())
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("verify opened %s: %w", label, statErr)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		_ = file.Close()
		return nil, fmt.Errorf("%s identity or size changed while opening", label)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.Join(fmt.Errorf("read %s: %w", label, readErr), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s: %w", label, closeErr)
	}
	if int64(len(contents)) != info.Size() {
		return nil, fmt.Errorf("%s size changed while reading", label)
	}
	return contents, nil
}

func verifyWorkerArtifact(label string, document workerArtifactDocument) (runtimelock.VerifiedFile, error) {
	info, err := os.Lstat(document.Path)
	if err != nil {
		return runtimelock.VerifiedFile{}, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return runtimelock.VerifiedFile{}, fmt.Errorf("%s is not a directly executable, deployment-immutable regular file: mode=%s", label, info.Mode())
	}
	if info.Size() != document.SizeBytes {
		return runtimelock.VerifiedFile{}, fmt.Errorf("%s size = %d, want %d", label, info.Size(), document.SizeBytes)
	}
	digest, size, err := runtimelock.HashFile(document.Path)
	if err != nil {
		return runtimelock.VerifiedFile{}, err
	}
	if digest != document.SHA256 || size != document.SizeBytes {
		return runtimelock.VerifiedFile{}, fmt.Errorf("%s does not match its deployment digest and size", label)
	}
	return runtimelock.VerifiedFile{Path: document.Path, SHA256: digest, SizeBytes: size}, nil
}

func newWorkerHTTPClient(document workerTLSDocument) (*http.Client, error) {
	certificateBytes, err := readBoundedWorkerFile("worker TLS certificate", document.CertificateFile, maximumWorkerTLSIdentityBytes)
	if err != nil {
		return nil, err
	}
	keyInfo, err := os.Lstat(document.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("inspect worker TLS private key: %w", err)
	}
	if keyInfo.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("worker TLS private key permissions are too broad: mode=%s", keyInfo.Mode())
	}
	keyBytes, err := readBoundedWorkerFile("worker TLS private key", document.KeyFile, maximumWorkerTLSIdentityBytes)
	if err != nil {
		return nil, err
	}
	defer clear(keyBytes)
	certificate, err := tls.X509KeyPair(certificateBytes, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("load worker TLS identity: %w", err)
	}
	caBytes, err := readBoundedWorkerFile("worker TLS CA bundle", document.CAFile, maximumWorkerCABundleBytes)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("worker TLS CA bundle contains no certificates")
	}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second, DisableCompression: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate},
		},
	}
	return &http.Client{Transport: transport}, nil
}

func finishWorkerJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}
