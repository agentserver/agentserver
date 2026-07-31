package harnesspool

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const (
	localWorkerBootstrapDescriptor  = 3
	localWorkerPromptDescriptor     = 4
	localWorkerCheckpointDescriptor = 5
)

var (
	lowercaseSHA256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	serviceAccountNamePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

// LocalProcessCredential is optional at the library boundary so unprivileged
// tests and explicit developer mode can run. Production assembly must provide
// the fixed worker identity; the app identity is sealed by harness-worker's
// final-exec trampoline.
type LocalProcessCredential struct {
	UID uint32
	GID uint32
}

type LocalProcessLauncherConfig struct {
	WorkerExecutable           string
	WorkerArguments            []string
	RuntimeRoot                string
	RuntimeCleaner             LocalAttemptRuntimeCleaner
	Environment                []string
	ObjectSource               AttemptObjectSource
	Credential                 *LocalProcessCredential
	ExpectedAppCredential      *LocalProcessCredential
	ExpectedWorkerImageDigest  string
	ExpectedServiceAccount     string
	InputWriteTimeout          time.Duration
	TerminateGrace             time.Duration
	ProcessGroupCleanupTimeout time.Duration
}

// AttemptObjectSource is implemented by the pool's encrypted object-store
// client. The launcher streams exact objects into one-shot worker pipes and
// never delegates object-store credentials to the worker.
type AttemptObjectSource interface {
	OpenRunObject(context.Context, runmanifest.ObjectPointer) (io.ReadCloser, error)
}

// LocalProcessLauncher starts a fresh worker process for every attempt. It
// never writes capabilities, signed manifests, or prompt bytes to argv, env,
// or a persisted file. Authority is sent once through descriptor 3 and the
// independently hashed prompt object through descriptor 4. A committed
// checkpoint, when present, is independently streamed through descriptor 5.
type LocalProcessLauncher struct {
	config LocalProcessLauncherConfig
}

func NewLocalProcessLauncher(config LocalProcessLauncherConfig) (*LocalProcessLauncher, error) {
	if err := validateLocalProcessLauncherConfig(config); err != nil {
		return nil, err
	}
	if config.RuntimeCleaner == nil {
		config.RuntimeCleaner = &localFilesystemAttemptRuntimeCleaner{runtimeRoot: config.RuntimeRoot}
	}
	config.WorkerArguments = append([]string(nil), config.WorkerArguments...)
	config.Environment = append([]string(nil), config.Environment...)
	if config.Credential != nil {
		credential := *config.Credential
		config.Credential = &credential
	}
	appCredential := *config.ExpectedAppCredential
	config.ExpectedAppCredential = &appCredential
	return &LocalProcessLauncher{config: config}, nil
}

func (launcher *LocalProcessLauncher) Launch(ctx context.Context, launch AttemptWorkloadLaunch) (AttemptWorkload, error) {
	if launcher == nil {
		return nil, errors.New("local process launcher is required")
	}
	if ctx == nil {
		return nil, errors.New("local process launch context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePreparedSupervisionInput(launch.Prepared.Scheduled, launch.Prepared); err != nil {
		return nil, fmt.Errorf("validate local worker launch: %w", err)
	}
	manifest := launch.Prepared.Manifest
	if manifest.WorkerImageDigest != launcher.config.ExpectedWorkerImageDigest ||
		manifest.ExpectedServiceAccount != launcher.config.ExpectedServiceAccount {
		return nil, errors.New("prepared launch does not match the local worker deployment profile")
	}
	bootstrap, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{
		Version: harnessbootstrap.CurrentVersion, SignedManifest: launch.Prepared.SignedManifest,
		ControlCapability: launch.ControlCapability, RuntimeCapabilities: launch.RuntimeCapabilities,
	})
	if err != nil {
		return nil, fmt.Errorf("encode local worker bootstrap: %w", err)
	}
	defer clear(bootstrap)
	promptObject, err := launcher.config.ObjectSource.OpenRunObject(ctx, manifest.Prompt)
	if err != nil {
		return nil, fmt.Errorf("open local worker prompt object: %w", err)
	}
	if promptObject == nil {
		return nil, errors.New("local worker prompt object source returned a nil reader")
	}
	promptObject = &onceReadCloser{ReadCloser: promptObject}
	defer promptObject.Close()
	var checkpointObject io.ReadCloser
	if manifest.PreviousCheckpoint != nil {
		checkpointObject, err = launcher.config.ObjectSource.OpenRunObject(ctx, manifest.PreviousCheckpoint.Object)
		if err != nil {
			return nil, fmt.Errorf("open local worker checkpoint object: %w", err)
		}
		if checkpointObject == nil {
			return nil, errors.New("local worker checkpoint object source returned a nil reader")
		}
		checkpointObject = &onceReadCloser{ReadCloser: checkpointObject}
		defer checkpointObject.Close()
	}

	runtimeName, err := allocateAttemptRuntimeName()
	if err != nil {
		return nil, err
	}
	runtimeDirectory := filepath.Join(launcher.config.RuntimeRoot, runtimeName)
	if err := os.Mkdir(runtimeDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create local attempt runtime: %w", err)
	}
	removeRuntime := true
	defer func() {
		if removeRuntime {
			_ = launcher.config.RuntimeCleaner.CleanLocalAttemptRuntime(runtimeDirectory)
		}
	}()
	if launcher.config.Credential == nil {
		// The worker and pool share an identity in explicit developer mode,
		// but the fixed app UID still needs execute-only traversal to reach
		// its private runtime below this anchor. 0701 reveals no directory
		// entries or file contents to that identity.
		if err := os.Chmod(runtimeDirectory, 0o701); err != nil {
			return nil, fmt.Errorf("grant local development app runtime traversal: %w", err)
		}
	} else {
		// Keep pool ownership so the holder can always remove the tree, while
		// granting the fixed worker primary group access to create its runtime.
		// Production's fixed-code threat model permits workers to share this
		// group; arbitrary local code requires a different sandbox backend.
		if err := os.Chown(runtimeDirectory, -1, int(launcher.config.Credential.GID)); err != nil {
			return nil, fmt.Errorf("assign local attempt runtime group: %w", err)
		}
		// The fixed app UID needs execute-only traversal to reach its 0700
		// attempt subdirectories after the worker creates them. No app-owned
		// file is readable at this anchor and arbitrary local code is outside
		// this backend's threat model.
		if err := os.Chmod(runtimeDirectory, 0o771); err != nil {
			return nil, fmt.Errorf("grant local worker runtime access: %w", err)
		}
	}
	runtimeAnchor, err := openLocalAttemptRuntimeAnchor(runtimeDirectory)
	if err != nil {
		return nil, err
	}
	anchorOwned := true
	defer func() {
		if anchorOwned {
			_ = runtimeAnchor.Close()
		}
	}()

	bootstrapReader, bootstrapWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create local worker bootstrap pipe: %w", err)
	}
	defer bootstrapReader.Close()
	defer bootstrapWriter.Close()
	promptReader, promptWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create local worker prompt pipe: %w", err)
	}
	defer promptReader.Close()
	defer promptWriter.Close()
	var checkpointReader, checkpointWriter *os.File
	if manifest.PreviousCheckpoint != nil {
		checkpointReader, checkpointWriter, err = os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("create local worker checkpoint pipe: %w", err)
		}
		defer checkpointReader.Close()
		defer checkpointWriter.Close()
	}

	arguments := make([]string, 0, len(launcher.config.WorkerArguments)+3)
	arguments = append(arguments, launcher.config.WorkerArguments...)
	arguments = append(arguments, fmt.Sprintf("--bootstrap-fd=%d", localWorkerBootstrapDescriptor))
	arguments = append(arguments, fmt.Sprintf("--prompt-fd=%d", localWorkerPromptDescriptor))
	if checkpointReader != nil {
		arguments = append(arguments, fmt.Sprintf("--checkpoint-fd=%d", localWorkerCheckpointDescriptor))
	}
	command := exec.Command(launcher.config.WorkerExecutable, arguments...)
	command.Dir = runtimeDirectory
	command.Env = append([]string(nil), launcher.config.Environment...)
	command.ExtraFiles = []*os.File{bootstrapReader, promptReader}
	if checkpointReader != nil {
		command.ExtraFiles = append(command.ExtraFiles, checkpointReader)
	}
	if err := configureLocalAttemptCommand(command, launcher.config.Credential); err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start local harness worker: %w", err)
	}
	_ = bootstrapReader.Close()
	_ = promptReader.Close()
	if checkpointReader != nil {
		_ = checkpointReader.Close()
	}

	workload := newLocalProcessWorkload(command, runtimeDirectory, runtimeAnchor, launcher.config)
	anchorOwned = false
	removeRuntime = false
	type inputWriteResult struct {
		name string
		err  error
	}
	inputCount := 2
	if checkpointWriter != nil {
		inputCount++
	}
	writeDone := make(chan inputWriteResult, inputCount)
	go func() {
		_, writeErr := bootstrapWriter.ReadFrom(bytes.NewReader(bootstrap))
		closeErr := bootstrapWriter.Close()
		writeDone <- inputWriteResult{name: "bootstrap", err: errors.Join(writeErr, closeErr)}
	}()
	go func() {
		writeDone <- inputWriteResult{
			name: "prompt", err: copyLocalRunObject(promptWriter, promptObject, manifest.Prompt),
		}
	}()
	if checkpointWriter != nil {
		go func() {
			writeDone <- inputWriteResult{
				name: "checkpoint",
				err:  copyLocalRunObject(checkpointWriter, checkpointObject, manifest.PreviousCheckpoint.Object),
			}
		}()
	}
	closeInputs := func() {
		_ = bootstrapWriter.Close()
		_ = promptWriter.Close()
		_ = promptObject.Close()
		if checkpointWriter != nil {
			_ = checkpointWriter.Close()
			_ = checkpointObject.Close()
		}
	}
	writeTimer := time.NewTimer(launcher.config.InputWriteTimeout)
	defer writeTimer.Stop()
	for pending := inputCount; pending > 0; pending-- {
		select {
		case result := <-writeDone:
			if result.err == nil {
				continue
			}
			closeInputs()
			abortErr := workload.abortLaunch()
			return nil, errors.Join(fmt.Errorf("write local worker %s input: %w", result.name, result.err), abortErr)
		case <-ctx.Done():
			closeInputs()
			abortErr := workload.abortLaunch()
			return nil, errors.Join(ctx.Err(), abortErr)
		case <-writeTimer.C:
			closeInputs()
			abortErr := workload.abortLaunch()
			return nil, errors.Join(
				fmt.Errorf("write local worker one-shot inputs: timeout after %s", launcher.config.InputWriteTimeout),
				abortErr,
			)
		}
	}
	if err := ctx.Err(); err != nil {
		abortErr := workload.abortLaunch()
		return nil, errors.Join(err, abortErr)
	}
	return workload, nil
}

type localProcessWorkload struct {
	command                    *exec.Cmd
	runtimeDirectory           string
	runtimeAnchor              *os.File
	runtimeCleaner             LocalAttemptRuntimeCleaner
	expectedAppCredential      LocalProcessCredential
	terminateGrace             time.Duration
	processGroupCleanupTimeout time.Duration
	stopped                    chan struct{}
	cleanupDone                chan struct{}
	cleanupOnce                sync.Once
	mu                         sync.Mutex
	result                     error
	cleanupStarted             bool
	cleanupErr                 error
}

func newLocalProcessWorkload(
	command *exec.Cmd,
	runtimeDirectory string,
	runtimeAnchor *os.File,
	config LocalProcessLauncherConfig,
) *localProcessWorkload {
	workload := &localProcessWorkload{
		command: command, runtimeDirectory: runtimeDirectory, runtimeAnchor: runtimeAnchor,
		runtimeCleaner: config.RuntimeCleaner, expectedAppCredential: *config.ExpectedAppCredential,
		terminateGrace:             config.TerminateGrace,
		processGroupCleanupTimeout: config.ProcessGroupCleanupTimeout,
		stopped:                    make(chan struct{}),
		cleanupDone:                make(chan struct{}),
	}
	go workload.reap()
	return workload
}

func (workload *localProcessWorkload) reap() {
	waitErr := workload.command.Wait()
	groupErr := forceAndWaitLocalAttemptGroup(
		workload.command.Process.Pid,
		workload.processGroupCleanupTimeout,
	)
	workload.mu.Lock()
	workload.result = errors.Join(waitErr, groupErr)
	workload.mu.Unlock()
	close(workload.stopped)
}

func (workload *localProcessWorkload) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local process wait context is required")
	}
	select {
	case <-workload.stopped:
		workload.mu.Lock()
		defer workload.mu.Unlock()
		return workload.result
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (workload *localProcessWorkload) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local process stop context is required")
	}
	select {
	case <-workload.stopped:
		return nil
	default:
	}
	termErr := signalLocalAttemptGroup(workload.command.Process.Pid, false)
	timer := time.NewTimer(workload.terminateGrace)
	defer timer.Stop()
	select {
	case <-workload.stopped:
		return termErr
	case <-ctx.Done():
		killErr := signalLocalAttemptGroup(workload.command.Process.Pid, true)
		return errors.Join(termErr, killErr, ctx.Err())
	case <-timer.C:
	}
	killErr := signalLocalAttemptGroup(workload.command.Process.Pid, true)
	select {
	case <-workload.stopped:
		return errors.Join(termErr, killErr)
	case <-ctx.Done():
		return errors.Join(termErr, killErr, ctx.Err())
	}
}

func (workload *localProcessWorkload) OpenCheckpointRollout(
	ctx context.Context,
	locator string,
) (AttemptCheckpointRollout, error) {
	if ctx == nil {
		return AttemptCheckpointRollout{}, errors.New("local checkpoint rollout context is required")
	}
	if err := checkpoint.ValidateRolloutLocator(locator); err != nil {
		return AttemptCheckpointRollout{}, err
	}
	select {
	case <-workload.stopped:
	case <-ctx.Done():
		return AttemptCheckpointRollout{}, ctx.Err()
	}
	workload.mu.Lock()
	defer workload.mu.Unlock()
	if workload.result != nil {
		return AttemptCheckpointRollout{}, errors.New("local attempt did not stop cleanly enough for checkpoint finalization")
	}
	if workload.cleanupStarted {
		return AttemptCheckpointRollout{}, errors.New("local attempt runtime cleanup has already started")
	}
	if err := ctx.Err(); err != nil {
		return AttemptCheckpointRollout{}, err
	}
	rollout, err := openLocalCheckpointRollout(
		workload.runtimeAnchor,
		locator,
		workload.expectedAppCredential,
	)
	if err != nil {
		return AttemptCheckpointRollout{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = rollout.Reader.Close()
		return AttemptCheckpointRollout{}, err
	}
	return rollout, nil
}

func (workload *localProcessWorkload) Cleanup(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local process cleanup context is required")
	}
	select {
	case <-workload.stopped:
	case <-ctx.Done():
		return ctx.Err()
	}
	workload.cleanupOnce.Do(func() {
		workload.mu.Lock()
		workload.cleanupStarted = true
		workload.mu.Unlock()
		go func() {
			cleanupErr := workload.runtimeCleaner.CleanLocalAttemptRuntime(workload.runtimeDirectory)
			closeErr := workload.runtimeAnchor.Close()
			workload.mu.Lock()
			workload.cleanupErr = errors.Join(cleanupErr, closeErr)
			workload.mu.Unlock()
			close(workload.cleanupDone)
		}()
	})
	select {
	case <-workload.cleanupDone:
		workload.mu.Lock()
		defer workload.mu.Unlock()
		return workload.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (workload *localProcessWorkload) abortLaunch() error {
	killErr := signalLocalAttemptGroup(workload.command.Process.Pid, true)
	waitContext, cancel := context.WithTimeout(
		context.Background(), workload.processGroupCleanupTimeout+time.Second,
	)
	waitErr := workload.Wait(waitContext)
	cancel()
	cleanupContext, cancelCleanup := context.WithTimeout(
		context.Background(), workload.processGroupCleanupTimeout+time.Second,
	)
	defer cancelCleanup()
	cleanupErr := workload.Cleanup(cleanupContext)
	return errors.Join(killErr, waitErr, cleanupErr)
}

func openLocalAttemptRuntimeAnchor(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect local attempt runtime anchor: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("local attempt runtime anchor is not a direct directory: mode=%s", before.Mode())
	}
	anchor, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local attempt runtime anchor: %w", err)
	}
	opened, openedErr := anchor.Stat()
	after, afterErr := os.Lstat(path)
	if openedErr != nil || afterErr != nil || !opened.IsDir() || !after.IsDir() ||
		after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = anchor.Close()
		return nil, errors.New("local attempt runtime anchor identity changed while opening")
	}
	return anchor, nil
}

func validateLocalProcessLauncherConfig(config LocalProcessLauncherConfig) error {
	if config.WorkerExecutable == "" || !filepath.IsAbs(config.WorkerExecutable) {
		return errors.New("local worker executable must be an absolute path")
	}
	executable, err := os.Lstat(config.WorkerExecutable)
	if err != nil {
		return fmt.Errorf("inspect local worker executable: %w", err)
	}
	if !executable.Mode().IsRegular() || executable.Mode()&os.ModeSymlink != 0 || executable.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("local worker executable is not a directly executable regular file: mode=%s", executable.Mode())
	}
	if config.RuntimeRoot == "" || !filepath.IsAbs(config.RuntimeRoot) {
		return errors.New("local attempt runtime root must be an absolute path")
	}
	if config.Credential != nil && (config.Credential.UID == 0 || config.Credential.GID == 0 ||
		config.Credential.UID == ^uint32(0) || config.Credential.GID == ^uint32(0)) {
		return errors.New("local worker credential must be a valid unprivileged identity")
	}
	if config.ExpectedAppCredential == nil || config.ExpectedAppCredential.UID == 0 ||
		config.ExpectedAppCredential.GID == 0 || config.ExpectedAppCredential.UID == ^uint32(0) ||
		config.ExpectedAppCredential.GID == ^uint32(0) {
		return errors.New("expected local app credential must be a valid unprivileged identity")
	}
	if config.Credential != nil && (config.Credential.UID == config.ExpectedAppCredential.UID ||
		config.Credential.GID == config.ExpectedAppCredential.GID) {
		return errors.New("local worker and app credentials must be distinct")
	}
	runtimeRoot, err := os.Lstat(config.RuntimeRoot)
	if err != nil {
		return fmt.Errorf("inspect local attempt runtime root: %w", err)
	}
	if !runtimeRoot.IsDir() || runtimeRoot.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local attempt runtime root is not a real directory: mode=%s", runtimeRoot.Mode())
	}
	if config.Credential == nil {
		if runtimeRoot.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("local attempt runtime root permissions are too broad for developer mode: mode=%s", runtimeRoot.Mode())
		}
	} else if runtimeRoot.Mode().Perm() != 0o711 {
		return fmt.Errorf("production local attempt runtime root must be mode 0711 for execute-only app traversal: mode=%s", runtimeRoot.Mode())
	}
	if config.Environment == nil {
		return errors.New("local worker environment must be explicit")
	}
	if config.ObjectSource == nil {
		return errors.New("local worker object source is required")
	}
	seenEnvironment := make(map[string]struct{}, len(config.Environment))
	for index, entry := range config.Environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid local worker environment entry at index %d", index)
		}
		if _, duplicate := seenEnvironment[name]; duplicate {
			return fmt.Errorf("duplicate local worker environment variable %q", name)
		}
		seenEnvironment[name] = struct{}{}
	}
	for index, argument := range config.WorkerArguments {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("local worker argument %d contains NUL", index)
		}
		if strings.HasPrefix(argument, "--bootstrap-fd") || strings.HasPrefix(argument, "--prompt-fd") ||
			strings.HasPrefix(argument, "--checkpoint-fd") {
			return errors.New("local worker arguments must not override inherited input descriptors")
		}
	}
	if config.Credential != nil && (config.RuntimeCleaner == nil || !config.RuntimeCleaner.productionSafeLocalAttemptCleaner()) {
		return errors.New("production local process launcher requires a verified privileged runtime cleaner")
	}
	if config.Credential != nil {
		if err := requireLocalProcessProductionCapabilities(); err != nil {
			return err
		}
	}
	if !lowercaseSHA256Pattern.MatchString(config.ExpectedWorkerImageDigest) {
		return errors.New("expected local worker image digest must be lowercase SHA-256")
	}
	if !serviceAccountNamePattern.MatchString(config.ExpectedServiceAccount) {
		return errors.New("expected local worker service account is invalid")
	}
	for field, duration := range map[string]time.Duration{
		"one-shot input write timeout":  config.InputWriteTimeout,
		"terminate grace":               config.TerminateGrace,
		"process-group cleanup timeout": config.ProcessGroupCleanupTimeout,
	} {
		if duration < time.Millisecond || duration > time.Minute {
			return fmt.Errorf("local worker %s must be between 1ms and 1m", field)
		}
	}
	return nil
}

type onceReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (reader *onceReadCloser) Close() error {
	reader.once.Do(func() { reader.err = reader.ReadCloser.Close() })
	return reader.err
}

func copyLocalRunObject(writer *os.File, source io.ReadCloser, pointer runmanifest.ObjectPointer) error {
	if writer == nil || source == nil {
		return errors.New("local worker object transfer requires a pipe and source")
	}
	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(writer, hash), source, pointer.SizeBytes)
	var extra [1]byte
	extraBytes, extraErr := source.Read(extra[:])
	sourceCloseErr := source.Close()
	writerCloseErr := writer.Close()
	if copyErr != nil || written != pointer.SizeBytes {
		return errors.Join(errors.New("local worker object source ended before its signed size"), copyErr, sourceCloseErr, writerCloseErr)
	}
	if extraBytes != 0 || !errors.Is(extraErr, io.EOF) {
		return errors.Join(errors.New("local worker object source exceeded its signed size"), extraErr, sourceCloseErr, writerCloseErr)
	}
	want, err := hex.DecodeString(pointer.SHA256)
	if err != nil || len(want) != sha256.Size || hex.EncodeToString(want) != pointer.SHA256 {
		return errors.Join(errors.New("local worker object pointer digest is invalid"), sourceCloseErr, writerCloseErr)
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), want) != 1 {
		return errors.Join(errors.New("local worker object source does not match its signed digest"), sourceCloseErr, writerCloseErr)
	}
	return errors.Join(sourceCloseErr, writerCloseErr)
}

func allocateAttemptRuntimeName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("allocate local attempt runtime name: %w", err)
	}
	return "attempt-" + hex.EncodeToString(random[:]), nil
}
