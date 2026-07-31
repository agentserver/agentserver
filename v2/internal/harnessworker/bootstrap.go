package harnessworker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

var workerInstanceIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type WorkerInstanceIDGenerator func() (string, error)

// VerifiedBootstrap is the complete immutable authority available to one
// short-lived worker after its inherited bootstrap pipe has been consumed and
// closed. ControlCapability must never be logged or passed to a child.
type VerifiedBootstrap struct {
	Manifest          runmanifest.Manifest
	SignedManifest    runmanifest.SignedManifest
	ControlCapability string
	WorkerInstanceID  string
}

// LoadBootstrap consumes exactly one inherited pipe through EOF, closes it
// before doing signature verification, and allocates a fresh process identity.
// The caller transfers ownership of bootstrapPipe even when this function
// returns an error.
func LoadBootstrap(
	bootstrapPipe *os.File,
	keyring *runmanifest.VerificationKeyring,
	generateWorkerInstanceID WorkerInstanceIDGenerator,
) (VerifiedBootstrap, error) {
	if bootstrapPipe == nil {
		return VerifiedBootstrap{}, errors.New("worker bootstrap pipe is required")
	}
	info, statErr := bootstrapPipe.Stat()
	if statErr != nil {
		_ = bootstrapPipe.Close()
		return VerifiedBootstrap{}, fmt.Errorf("inspect worker bootstrap pipe: %w", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		_ = bootstrapPipe.Close()
		return VerifiedBootstrap{}, errors.New("worker bootstrap descriptor must be a pipe")
	}

	envelope, readErr := harnessbootstrap.Read(bootstrapPipe)
	closeErr := bootstrapPipe.Close()
	if readErr != nil || closeErr != nil {
		return VerifiedBootstrap{}, errors.Join(readErr, closeErr)
	}
	manifest, err := keyring.Verify(envelope.SignedManifest)
	if err != nil {
		return VerifiedBootstrap{}, fmt.Errorf("verify worker run manifest: %w", err)
	}
	if generateWorkerInstanceID == nil {
		generateWorkerInstanceID = NewWorkerInstanceID
	}
	workerInstanceID, err := generateWorkerInstanceID()
	if err != nil {
		return VerifiedBootstrap{}, fmt.Errorf("allocate worker instance ID: %w", err)
	}
	if err := validateWorkerInstanceID(workerInstanceID); err != nil {
		return VerifiedBootstrap{}, err
	}
	signed := envelope.SignedManifest
	signed.Manifest = append(json.RawMessage(nil), signed.Manifest...)
	return VerifiedBootstrap{
		Manifest: manifest, SignedManifest: signed,
		ControlCapability: envelope.ControlCapability,
		WorkerInstanceID:  workerInstanceID,
	}, nil
}

func NewWorkerInstanceID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read worker instance randomness: %w", err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validateWorkerInstanceID(value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !workerInstanceIDPattern.MatchString(value) {
		return errors.New("worker instance ID must be a non-zero canonical lowercase UUID")
	}
	return nil
}
