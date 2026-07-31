package harnesspool

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestHarnessWorkerLoadsVerifiedBootstrapAndClosesPipe(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	capability := fixedControlCapability(7)
	raw, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{
		Version: harnessbootstrap.CurrentVersion, SignedManifest: prepared.SignedManifest,
		ControlCapability: capability, RuntimeCapabilities: testLocalRuntimeCapabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	writeResult := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(writer, bytes.NewReader(raw))
		writeResult <- errors.Join(writeErr, writer.Close())
	}()

	workerID := "84000000-0000-4000-8000-000000000084"
	loaded, err := harnessworker.LoadBootstrap(reader, launchPreparerVerificationKeyring(t), func() (string, error) {
		return workerID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Manifest, prepared.Manifest) ||
		!bytes.Equal(loaded.SignedManifest.Manifest, prepared.SignedManifest.Manifest) ||
		loaded.ControlCapability != capability || loaded.ExecutorMCPCapability != testLocalRuntimeCapabilities().ExecutorMCP ||
		loaded.LLMProxyCapability != testLocalRuntimeCapabilities().LLMProxy || loaded.WorkerInstanceID != workerID {
		t.Fatalf("loaded bootstrap = %+v", loaded)
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("LoadBootstrap left the inherited pipe open")
	}
}

func TestHarnessWorkerBootstrapRejectsNonPipeAndUntrustedManifest(t *testing.T) {
	t.Run("non-pipe", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "bootstrap")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harnessworker.LoadBootstrap(file, nil, nil); err == nil || !strings.Contains(err.Error(), "must be a pipe") {
			t.Fatalf("LoadBootstrap() error = %v", err)
		}
		if _, err := file.Stat(); err == nil {
			t.Fatal("rejected bootstrap file was not closed")
		}
	})

	t.Run("untrusted signature", func(t *testing.T) {
		prepared := poolTestPreparedLaunch(t)
		raw, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{
			Version: harnessbootstrap.CurrentVersion, SignedManifest: prepared.SignedManifest,
			ControlCapability: fixedControlCapability(7), RuntimeCapabilities: testLocalRuntimeCapabilities(),
		})
		if err != nil {
			t.Fatal(err)
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_, _ = io.Copy(writer, bytes.NewReader(raw))
			_ = writer.Close()
		}()
		untrusted := verificationKeyringForSeed(t, "different-key", "different-seed")
		if _, err := harnessworker.LoadBootstrap(reader, untrusted, nil); err == nil || !strings.Contains(err.Error(), "not trusted") {
			t.Fatalf("LoadBootstrap() error = %v", err)
		}
		if _, err := reader.Stat(); err == nil {
			t.Fatal("untrusted bootstrap pipe was not closed")
		}
	})
}

func TestHarnessWorkerInstanceIDsAreFreshCanonicalV4(t *testing.T) {
	seen := make(map[string]struct{})
	for range 128 {
		workerID, err := harnessworker.NewWorkerInstanceID()
		if err != nil {
			t.Fatal(err)
		}
		if len(workerID) != 36 || workerID[14] != '4' || !strings.Contains("89ab", string(workerID[19])) {
			t.Fatalf("worker instance ID = %q", workerID)
		}
		if _, duplicate := seen[workerID]; duplicate {
			t.Fatalf("duplicate worker instance ID %q", workerID)
		}
		seen[workerID] = struct{}{}
	}
}

func launchPreparerVerificationKeyring(t *testing.T) *runmanifest.VerificationKeyring {
	t.Helper()
	return verificationKeyringForSeed(t, "cluster-key-1", "launch-preparer-key")
}

func verificationKeyringForSeed(t *testing.T, keyID, source string) *runmanifest.VerificationKeyring {
	t.Helper()
	seed := sha256.Sum256([]byte(source))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	document := runmanifest.VerificationKeyringDocument{
		Version: runmanifest.VerificationKeyringVersion,
		Keys: []runmanifest.VerificationKeyDocument{{
			KeyID: keyID, Algorithm: runmanifest.SignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		}},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := runmanifest.ParseVerificationKeyring(raw)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}
