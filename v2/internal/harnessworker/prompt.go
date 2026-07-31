package harnessworker

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const (
	PromptMediaType    = "text/plain; charset=utf-8"
	MaximumPromptBytes = 1024 * 1024
)

// LoadPrompt consumes and closes the dedicated one-shot prompt pipe, then
// verifies the exact signed object pointer before returning model-visible
// text. The object bytes never enter argv, the worker environment, or config.
func LoadPrompt(promptPipe *os.File, pointer runmanifest.ObjectPointer) (string, error) {
	if promptPipe == nil {
		return "", errors.New("worker prompt pipe is required")
	}
	info, statErr := promptPipe.Stat()
	if statErr != nil {
		_ = promptPipe.Close()
		return "", fmt.Errorf("inspect worker prompt pipe: %w", statErr)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		_ = promptPipe.Close()
		return "", errors.New("worker prompt descriptor must be a pipe")
	}
	if pointer.MediaType != PromptMediaType {
		_ = promptPipe.Close()
		return "", fmt.Errorf("worker prompt media type must be %q", PromptMediaType)
	}
	if pointer.SizeBytes < 1 || pointer.SizeBytes > MaximumPromptBytes {
		_ = promptPipe.Close()
		return "", fmt.Errorf("worker prompt size must be between 1 and %d bytes", MaximumPromptBytes)
	}
	wantDigest, err := decodePromptDigest(pointer.SHA256)
	if err != nil {
		_ = promptPipe.Close()
		return "", err
	}
	contents, readErr := io.ReadAll(io.LimitReader(promptPipe, pointer.SizeBytes+1))
	closeErr := promptPipe.Close()
	if readErr != nil || closeErr != nil {
		return "", errors.Join(readErr, closeErr)
	}
	if int64(len(contents)) != pointer.SizeBytes {
		return "", errors.New("worker prompt bytes do not match the signed object size")
	}
	digest := sha256.Sum256(contents)
	if subtle.ConstantTimeCompare(digest[:], wantDigest[:]) != 1 {
		return "", errors.New("worker prompt bytes do not match the signed object digest")
	}
	prompt := string(contents)
	if err := validateText("worker prompt", prompt, MaximumPromptBytes); err != nil {
		return "", err
	}
	return prompt, nil
}

func decodePromptDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
		return digest, errors.New("worker prompt object digest must be canonical lowercase SHA-256")
	}
	copy(digest[:], decoded)
	return digest, nil
}
