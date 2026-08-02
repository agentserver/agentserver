// Package harnessinit contains the two privileged, one-shot initialization
// boundaries used by the production harness Pod. It is not a runtime service.
package harnessinit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	workerDirectDirectoryMode = 0o500
	workerDirectFileMode      = 0o400

	ProfileCore            = "core"
	ProfileBrowserGateway  = "browser-gateway"
	ProfileExecutorGateway = "executor-gateway"
	ProfileHarnessWorker   = "harness-worker"
	ProfileLLMProxy        = "llmproxy"
)

type workerMaterial struct {
	name    string
	maximum int64
	content []byte
}

var materialProfiles = map[string][]workerMaterial{
	ProfileCore: {
		{name: "ca.crt", maximum: 1024 * 1024},
		{name: "tls.crt", maximum: 1024 * 1024},
		{name: "tls.key", maximum: 1024 * 1024},
		{name: "run-capability.key", maximum: 4 * 1024},
		{name: "run-capability-keyring.json", maximum: 64 * 1024},
		{name: "executor-enrollment.key", maximum: 4 * 1024},
	},
	ProfileBrowserGateway: {
		{name: "ca.crt", maximum: 1024 * 1024},
		{name: "tls.crt", maximum: 1024 * 1024},
		{name: "tls.key", maximum: 1024 * 1024},
	},
	ProfileExecutorGateway: {
		{name: "ca.crt", maximum: 1024 * 1024},
		{name: "tls.crt", maximum: 1024 * 1024},
		{name: "tls.key", maximum: 1024 * 1024},
		{name: "run-capability-keyring.json", maximum: 64 * 1024},
	},
	ProfileHarnessWorker: {
		{name: "ca.crt", maximum: 1024 * 1024},
		{name: "tls.crt", maximum: 1024 * 1024},
		{name: "tls.key", maximum: 1024 * 1024},
		{name: "run-manifest-keyring.json", maximum: 64 * 1024},
	},
	ProfileLLMProxy: {
		{name: "ca.crt", maximum: 1024 * 1024},
		{name: "tls.crt", maximum: 1024 * 1024},
		{name: "tls.key", maximum: 1024 * 1024},
		{name: "run-capability-keyring.json", maximum: 64 * 1024},
		{name: "upstream-ca.crt", maximum: 1024 * 1024},
		{name: "upstream-credential", maximum: 64 * 1024},
	},
}

// MaterializeWorkerFiles copies exactly the worker's CA, TLS identity, and
// public run-manifest keyring from a Kubernetes projected Secret into direct
// regular files owned only by the fixed worker UID/GID. Standard projected
// files are symlinks; the worker deliberately refuses those mutable paths.
func MaterializeWorkerFiles(sourceRoot, destination string, uid, gid uint32) error {
	return MaterializeFiles(ProfileHarnessWorker, sourceRoot, destination, uid, gid)
}

// MaterializeFiles publishes one closed deployment profile. The source may be
// a Kubernetes projected volume assembled from multiple Secrets/ConfigMaps;
// the destination is one immutable direct-file view for the service UID.
func MaterializeFiles(profile, sourceRoot, destination string, uid, gid uint32) error {
	profileFiles, found := materialProfiles[profile]
	if !found {
		return fmt.Errorf("unknown materialization profile %q", profile)
	}
	if uid == 0 || gid == 0 || uid > 1<<31-1 || gid > 1<<31-1 {
		return errors.New("worker material UID and GID must be unprivileged signed-32-bit identities")
	}
	if err := validateDirectDirectory("worker material source", sourceRoot, false); err != nil {
		return err
	}
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || filepath.Base(destination) == "." {
		return errors.New("worker material destination must be an absolute clean child path")
	}
	parent := filepath.Dir(destination)
	if err := validateDirectDirectory("worker material destination parent", parent, true); err != nil {
		return err
	}

	materials := make([]workerMaterial, len(profileFiles))
	for index, fileProfile := range profileFiles {
		content, err := readProjectedFile(sourceRoot, fileProfile.name, fileProfile.maximum)
		if err != nil {
			clearWorkerMaterials(materials)
			return fmt.Errorf("read projected %s material %s: %w", profile, fileProfile.name, err)
		}
		materials[index] = workerMaterial{name: fileProfile.name, maximum: fileProfile.maximum, content: content}
	}
	defer clearWorkerMaterials(materials)

	if _, err := os.Lstat(destination); err == nil {
		return verifyMaterializedWorkerFiles(destination, uid, gid, materials)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect worker material destination: %w", err)
	}

	temporary, err := os.MkdirTemp(parent, ".agentserver-worker-material-")
	if err != nil {
		return fmt.Errorf("create worker material staging directory: %w", err)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return fmt.Errorf("restrict worker material staging directory: %w", err)
	}
	for _, material := range materials {
		path := filepath.Join(temporary, material.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create direct worker %s: %w", material.name, err)
		}
		written, writeErr := file.Write(material.content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || written != len(material.content) {
			return errors.Join(
				fmt.Errorf("write direct worker %s", material.name), writeErr, syncErr, closeErr,
			)
		}
		if err := os.Chmod(path, workerDirectFileMode); err != nil {
			return fmt.Errorf("restrict direct worker %s: %w", material.name, err)
		}
		if err := ensureOwnership(path, uid, gid); err != nil {
			return fmt.Errorf("own direct worker %s: %w", material.name, err)
		}
	}
	if err := os.Chmod(temporary, workerDirectDirectoryMode); err != nil {
		return fmt.Errorf("seal worker material directory: %w", err)
	}
	if err := ensureOwnership(temporary, uid, gid); err != nil {
		return fmt.Errorf("own worker material directory: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		if _, inspectErr := os.Lstat(destination); inspectErr == nil {
			if verifyErr := verifyMaterializedWorkerFiles(destination, uid, gid, materials); verifyErr == nil {
				return nil
			}
		}
		return fmt.Errorf("publish direct worker material: %w", err)
	}
	keepTemporary = true
	if err := syncDirectory(parent); err != nil {
		return err
	}
	return verifyMaterializedWorkerFiles(destination, uid, gid, materials)
}

func validateDirectDirectory(label, path string, private bool) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s must be an absolute clean path", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a direct directory", label)
	}
	if private && info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must not be writable by group or other", label)
	}
	return nil
}

func verifyMaterializedWorkerFiles(root string, uid, gid uint32, materials []workerMaterial) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect direct worker material directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != workerDirectDirectoryMode {
		return errors.New("direct worker material directory has the wrong type or mode")
	}
	if err := verifyOwnership(info, uid, gid); err != nil {
		return fmt.Errorf("direct worker material directory ownership: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("list direct worker material directory: %w", err)
	}
	if len(entries) != len(materials) {
		return errors.New("direct worker material directory contains an unexpected file set")
	}
	materialByName := make(map[string]workerMaterial, len(materials))
	for _, material := range materials {
		materialByName[material.name] = material
	}
	for _, entry := range entries {
		material, found := materialByName[entry.Name()]
		if !found {
			return errors.New("direct worker material directory contains an unexpected file")
		}
		path := filepath.Join(root, material.name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect direct worker %s: %w", material.name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != workerDirectFileMode || info.Size() != int64(len(material.content)) {
			return fmt.Errorf("direct worker %s has the wrong type, mode, or size", material.name)
		}
		if err := verifyOwnership(info, uid, gid); err != nil {
			return fmt.Errorf("direct worker %s ownership: %w", material.name, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open direct worker %s: %w", material.name, err)
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, material.maximum+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, material.content) {
			clear(actual)
			return errors.Join(fmt.Errorf("direct worker %s differs from its projected source", material.name), readErr, closeErr)
		}
		clear(actual)
	}
	return nil
}

func ensureOwnership(path string, uid, gid uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	actualUID, actualGID, err := fileOwnership(info)
	if err != nil {
		return err
	}
	if actualUID == uid && actualGID == gid {
		return nil
	}
	return os.Chown(path, int(uid), int(gid))
}

func verifyOwnership(info os.FileInfo, uid, gid uint32) error {
	actualUID, actualGID, err := fileOwnership(info)
	if err != nil {
		return err
	}
	if actualUID != uid || actualGID != gid {
		return fmt.Errorf("uid:gid is %d:%d, want %d:%d", actualUID, actualGID, uid, gid)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open worker material parent for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("sync worker material parent"), syncErr, closeErr)
	}
	return nil
}

func clearWorkerMaterials(materials []workerMaterial) {
	for index := range materials {
		clear(materials[index].content)
	}
}
