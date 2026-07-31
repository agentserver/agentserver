//go:build linux || darwin

package harnesspool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/ucarion/jcs"
)

const (
	localWorkerHelperEnvironment = "AGENTSERVER_V2_LOCAL_WORKER_HELPER"
	localWorkerHelperMode        = "AGENTSERVER_V2_LOCAL_WORKER_MODE"
	localWorkerExpectedAttempt   = "AGENTSERVER_V2_LOCAL_WORKER_ATTEMPT"
	localWorkerDescendant        = "AGENTSERVER_V2_LOCAL_WORKER_DESCENDANT"
)

func TestLocalProcessLauncherPassesOneShotBootstrapAndCleansRuntime(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	capability := testLocalControlCapability()
	launcher, runtimeRoot := newLocalProcessLauncherForTest(t, prepared, "exit")
	workload, err := launcher.Launch(t.Context(), AttemptWorkloadLaunch{
		Prepared: prepared, ControlCapability: capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	local, ok := workload.(*localProcessWorkload)
	if !ok {
		t.Fatalf("local workload type = %T", workload)
	}
	if filepath.Dir(local.runtimeDirectory) != runtimeRoot {
		t.Fatalf("runtime directory = %q, want beneath %q", local.runtimeDirectory, runtimeRoot)
	}
	if err := workload.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(local.runtimeDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleaned runtime stat error = %v", err)
	}
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("runtime root retained %d entries", len(entries))
	}
}

func TestLocalProcessLauncherStopsWholeAttemptProcessGroup(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	launcher, _ := newLocalProcessLauncherForTest(t, prepared, "block-with-descendant")
	workload, err := launcher.Launch(t.Context(), AttemptWorkloadLaunch{
		Prepared: prepared, ControlCapability: testLocalControlCapability(),
	})
	if err != nil {
		t.Fatal(err)
	}
	local := workload.(*localProcessWorkload)
	readyPath := filepath.Join(local.runtimeDirectory, "ready")
	waitForLocalWorkerMarker(t, readyPath)
	info, err := os.Stat(local.runtimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("attempt runtime permissions = %o", info.Mode().Perm())
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := workload.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	waitErr := workload.Wait(t.Context())
	if waitErr == nil || (!strings.Contains(waitErr.Error(), "signal") && !strings.Contains(waitErr.Error(), "killed")) {
		t.Fatalf("stopped worker wait error = %v", waitErr)
	}
	if strings.Contains(waitErr.Error(), "process group remained alive") {
		t.Fatalf("descendant escaped process-group cleanup: %v", waitErr)
	}
	if _, err := os.Stat(local.runtimeDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped runtime stat error = %v", err)
	}
}

func TestLocalProcessLauncherRejectsProfileDriftBeforeFork(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	launcher, runtimeRoot := newLocalProcessLauncherForTest(t, prepared, "exit")
	prepared.Manifest.WorkerImageDigest = strings.Repeat("d", 64)
	_, err := launcher.Launch(t.Context(), AttemptWorkloadLaunch{
		Prepared: prepared, ControlCapability: testLocalControlCapability(),
	})
	if err == nil || !strings.Contains(err.Error(), "prepared manifest") {
		t.Fatalf("profile drift error = %v", err)
	}
	entries, readErr := os.ReadDir(runtimeRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("profile drift created %d runtime entries", len(entries))
	}
}

func TestLocalProcessLauncherConfigRejectsAmbientOrUnsafeInputs(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	valid := LocalProcessLauncherConfig{
		WorkerExecutable: executable, WorkerArguments: []string{"-test.run=none", "--"},
		RuntimeRoot: secureLocalRuntimeRoot(t), Environment: []string{},
		ExpectedWorkerImageDigest: strings.Repeat("c", 64), ExpectedServiceAccount: "harness-worker",
		BootstrapWriteTimeout: time.Second, TerminateGrace: 10 * time.Millisecond,
		ProcessGroupCleanupTimeout: time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*LocalProcessLauncherConfig)
		want   string
	}{
		{name: "relative executable", mutate: func(c *LocalProcessLauncherConfig) { c.WorkerExecutable = "worker" }, want: "absolute"},
		{name: "implicit environment", mutate: func(c *LocalProcessLauncherConfig) { c.Environment = nil }, want: "explicit"},
		{name: "duplicate environment", mutate: func(c *LocalProcessLauncherConfig) { c.Environment = []string{"A=1", "A=2"} }, want: "duplicate"},
		{name: "bootstrap override", mutate: func(c *LocalProcessLauncherConfig) { c.WorkerArguments = []string{"--bootstrap-fd=9"} }, want: "must not override"},
		{name: "invalid image digest", mutate: func(c *LocalProcessLauncherConfig) { c.ExpectedWorkerImageDigest = "ABC" }, want: "SHA-256"},
		{name: "invalid service account", mutate: func(c *LocalProcessLauncherConfig) { c.ExpectedServiceAccount = "Harness_Worker" }, want: "service account"},
		{name: "root credential", mutate: func(c *LocalProcessLauncherConfig) { c.Credential = &LocalProcessCredential{UID: 0, GID: 1} }, want: "unprivileged"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := NewLocalProcessLauncher(config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewLocalProcessLauncher() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := NewLocalProcessLauncher(valid); err != nil {
		t.Fatalf("valid local launcher config: %v", err)
	}
}

func TestLocalWorkerBootstrapIsCanonicalAndClosedWorld(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	capability := testLocalControlCapability()
	raw, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{
		Version: harnessbootstrap.CurrentVersion, SignedManifest: prepared.SignedManifest,
		ControlCapability: capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := harnessbootstrap.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ControlCapability != capability || !bytes.Equal(decoded.SignedManifest.Manifest, prepared.SignedManifest.Manifest) {
		t.Fatal("decoded bootstrap changed attempt authority")
	}
	if _, err := harnessbootstrap.Decode(append(append([]byte(nil), raw...), '\n')); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("non-canonical bootstrap error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["futureAuthority"] = true
	unknown, err := jcs.Append(nil, value)
	if err != nil {
		t.Fatal(err)
	}
	_, err = harnessbootstrap.Decode(unknown)
	if err == nil || !strings.Contains(err.Error(), "unknown field") || strings.Contains(err.Error(), capability) {
		t.Fatalf("unknown bootstrap field error = %v", err)
	}
}

// TestLocalProcessWorkerHelper is re-executed by the launcher tests as the
// pinned worker binary. It proves the capability is present on descriptor 3
// while absent from argv and environment.
func TestLocalProcessWorkerHelper(t *testing.T) {
	if os.Getenv(localWorkerHelperEnvironment) != "1" {
		return
	}
	if os.Getenv(localWorkerDescendant) == "1" {
		ignoreLocalWorkerTermination()
		blockLocalWorkerHelper()
	}
	bootstrapFile := os.NewFile(localWorkerBootstrapDescriptor, "harness-bootstrap")
	if bootstrapFile == nil {
		t.Fatal("bootstrap descriptor was not inherited")
	}
	envelope, err := harnessbootstrap.Read(bootstrapFile)
	closeErr := bootstrapFile.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read bootstrap: %v", errors.Join(err, closeErr))
	}
	manifest, err := runmanifest.ParseCanonical(envelope.SignedManifest.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RunAttemptID != os.Getenv(localWorkerExpectedAttempt) {
		t.Fatalf("bootstrap attempt = %q", manifest.RunAttemptID)
	}
	for _, argument := range os.Args {
		if strings.Contains(argument, envelope.ControlCapability) {
			t.Fatal("control capability leaked into argv")
		}
	}
	for _, environment := range os.Environ() {
		if strings.Contains(environment, envelope.ControlCapability) {
			t.Fatal("control capability leaked into environment")
		}
	}
	if os.Getenv(localWorkerHelperMode) == "exit" {
		return
	}
	if os.Getenv(localWorkerHelperMode) != "block-with-descendant" {
		t.Fatalf("unknown helper mode %q", os.Getenv(localWorkerHelperMode))
	}
	ignoreLocalWorkerTermination()
	descendant := exec.Command(os.Args[0], "-test.run=^TestLocalProcessWorkerHelper$")
	descendant.Env = append(os.Environ(), localWorkerDescendant+"=1")
	if err := descendant.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("ready", []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockLocalWorkerHelper()
}

func newLocalProcessLauncherForTest(t *testing.T, prepared PreparedRunLaunch, mode string) (*LocalProcessLauncher, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := secureLocalRuntimeRoot(t)
	launcher, err := NewLocalProcessLauncher(LocalProcessLauncherConfig{
		WorkerExecutable: executable,
		WorkerArguments:  []string{"-test.run=^TestLocalProcessWorkerHelper$", "--"},
		RuntimeRoot:      runtimeRoot,
		Environment: []string{
			localWorkerHelperEnvironment + "=1",
			localWorkerHelperMode + "=" + mode,
			localWorkerExpectedAttempt + "=" + prepared.Manifest.RunAttemptID,
		},
		ExpectedWorkerImageDigest:  prepared.Manifest.WorkerImageDigest,
		ExpectedServiceAccount:     prepared.Manifest.ExpectedServiceAccount,
		BootstrapWriteTimeout:      time.Second,
		TerminateGrace:             20 * time.Millisecond,
		ProcessGroupCleanupTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return launcher, runtimeRoot
}

func testLocalControlCapability() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
}

func waitForLocalWorkerMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worker marker %q was not created", path)
}

func secureLocalRuntimeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func ignoreLocalWorkerTermination() {
	// signal.Ignore is kept behind this helper so production launcher code does
	// not acquire signal package state. The test forces the SIGKILL fallback.
	signalIgnore(syscall.SIGTERM)
}

func blockLocalWorkerHelper() {
	for {
		time.Sleep(time.Hour)
	}
}
