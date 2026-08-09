package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"code.byted.org/paas/cloud-sdk-go/aksk"
)

const (
	credentialFileMaximumBytes = int64(4096)
	credentialFileModeMask     = os.FileMode(0o022)
)

// byteCloudCredentials is intentionally private to the provider process. It
// is never copied into an environment map, request metadata, or an error.
type byteCloudCredentials struct {
	accessKeyID     string
	secretAccessKey string
}

// loadByteCloudCredentials reads application credentials from two projected
// Kubernetes Secret files. The files are treated as immutable material: they
// must be regular, non-symlink files with no group/world write permission,
// and the path must not change while it is being read.
func loadByteCloudCredentials(accessKeyPath, secretKeyPath string) (byteCloudCredentials, error) {
	accessKeyID, err := readCredentialFile(accessKeyPath, "ByteCloud access key file")
	if err != nil {
		return byteCloudCredentials{}, err
	}
	secretAccessKey, err := readCredentialFile(secretKeyPath, "ByteCloud secret key file")
	if err != nil {
		return byteCloudCredentials{}, err
	}
	if err := aksk.ValidateAccessKeyID(accessKeyID); err != nil {
		return byteCloudCredentials{}, errors.New("ByteCloud application access key is invalid")
	}
	if secretAccessKey == "" || strings.TrimSpace(secretAccessKey) != secretAccessKey ||
		strings.ContainsAny(secretAccessKey, "\r\n\x00") {
		return byteCloudCredentials{}, errors.New("ByteCloud application secret key is invalid")
	}
	return byteCloudCredentials{accessKeyID: accessKeyID, secretAccessKey: secretAccessKey}, nil
}

func readCredentialFile(path, description string) (string, error) {
	if !validCredentialPath(path) {
		return "", fmt.Errorf("%s path must be an absolute clean path", description)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: credential file is unavailable", description)
	}
	if err := validateCredentialFileInfo(before); err != nil {
		return "", fmt.Errorf("read %s: %w", description, err)
	}
	if before.Size() > credentialFileMaximumBytes {
		return "", fmt.Errorf("read %s: credential file is too large", description)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: credential file is unavailable", description)
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("read %s: credential file metadata is unavailable", description)
	}
	if err := validateCredentialFileInfo(opened); err != nil {
		return "", fmt.Errorf("read %s: %w", description, err)
	}
	if !sameCredentialFile(before, opened) {
		return "", fmt.Errorf("read %s: credential file changed while opening", description)
	}

	data, err := io.ReadAll(io.LimitReader(file, credentialFileMaximumBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: credential file could not be read", description)
	}
	if int64(len(data)) > credentialFileMaximumBytes {
		return "", fmt.Errorf("read %s: credential file is too large", description)
	}

	// Stat the pathname again after reading. This catches a Secret rotation or
	// replacement that happened during the read; the descriptor itself still
	// refers to the original inode, so returning it would otherwise be
	// surprising and could mix generations of a credential pair.
	after, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: credential file changed while reading", description)
	}
	if err := validateCredentialFileInfo(after); err != nil || !sameCredentialFile(before, after) {
		return "", fmt.Errorf("read %s: credential file changed while reading", description)
	}
	value := string(data)
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("read %s: credential file contains invalid whitespace", description)
	}
	return value, nil
}

func validCredentialPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, '\x00')
}

func validateCredentialFileInfo(info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() {
		return errors.New("credential file must be a regular file")
	}
	if info.Mode()&credentialFileModeMask != 0 {
		return errors.New("credential file must not be group/world writable")
	}
	return nil
}

func sameCredentialFile(left, right os.FileInfo) bool {
	if left == nil || right == nil || !os.SameFile(left, right) {
		return false
	}
	return left.Mode().Perm() == right.Mode().Perm() && left.Size() == right.Size()
}
