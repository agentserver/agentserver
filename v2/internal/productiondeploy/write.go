package productiondeploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	bundleDirectoryMode = 0o555
	bundleFileMode      = 0o444
)

// WriteBundle atomically publishes a read-only deployment directory. An exact
// retry verifies every byte and mode; a differing existing directory is never
// overwritten or partially updated.
func WriteBundle(bundle Bundle, destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || filepath.Base(destination) == "." {
		return errors.New("production bundle destination must be an absolute clean child path")
	}
	if err := validateBundle(bundle); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("production bundle parent must be a direct directory not writable by group or other")
	}
	if _, err := os.Lstat(destination); err == nil {
		return verifyBundleDirectory(bundle, destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect production bundle destination: %w", err)
	}

	temporary, err := os.MkdirTemp(parent, ".agentserver-production-")
	if err != nil {
		return fmt.Errorf("create production bundle staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	for _, rendered := range bundle.Files {
		path := filepath.Join(temporary, rendered.Name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create production bundle file %s: %w", rendered.Name, err)
		}
		written, writeErr := file.Write(rendered.Content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || written != len(rendered.Content) {
			return errors.Join(fmt.Errorf("write production bundle file %s", rendered.Name), writeErr, syncErr, closeErr)
		}
		if err := os.Chmod(path, bundleFileMode); err != nil {
			return fmt.Errorf("seal production bundle file %s: %w", rendered.Name, err)
		}
	}
	if err := os.Chmod(temporary, bundleDirectoryMode); err != nil {
		return fmt.Errorf("seal production bundle directory: %w", err)
	}
	if err := syncBundleDirectory(temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		if _, inspectErr := os.Lstat(destination); inspectErr == nil {
			if verifyErr := verifyBundleDirectory(bundle, destination); verifyErr == nil {
				return nil
			}
		}
		return fmt.Errorf("publish production bundle: %w", err)
	}
	published = true
	if err := syncBundleDirectory(parent); err != nil {
		return err
	}
	return verifyBundleDirectory(bundle, destination)
}

func validateBundle(bundle Bundle) error {
	if len(bundle.Files) != 7 {
		return errors.New("production bundle must contain exactly seven rendered files")
	}
	wantNames := map[string]struct{}{
		foundationFile: {}, hydraMigrationFile: {}, migrationFile: {}, hydraSetupFile: {},
		bootstrapFile: {}, runtimeFile: {}, checksumsFile: {},
	}
	for _, file := range bundle.Files {
		if _, found := wantNames[file.Name]; !found || filepath.Base(file.Name) != file.Name || len(file.Content) == 0 || sha256Hex(file.Content) != file.SHA256 {
			return fmt.Errorf("production bundle contains invalid file %q", file.Name)
		}
		delete(wantNames, file.Name)
	}
	if len(wantNames) != 0 {
		return errors.New("production bundle is missing a required file")
	}
	return nil
}

func verifyBundleDirectory(bundle Bundle, root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != bundleDirectoryMode {
		return errors.New("existing production bundle directory has the wrong type or mode")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != len(bundle.Files) {
		return errors.New("existing production bundle has an unexpected file set")
	}
	wanted := make(map[string]RenderedFile, len(bundle.Files))
	for _, file := range bundle.Files {
		wanted[file.Name] = file
	}
	for _, entry := range entries {
		rendered, found := wanted[entry.Name()]
		if !found {
			return fmt.Errorf("existing production bundle contains unexpected file %s", entry.Name())
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != bundleFileMode || info.Size() != int64(len(rendered.Content)) {
			return fmt.Errorf("existing production bundle file %s has the wrong type, mode, or size", entry.Name())
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(rendered.Content))+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, rendered.Content) {
			return errors.Join(fmt.Errorf("existing production bundle file %s differs", entry.Name()), readErr, closeErr)
		}
	}
	return nil
}

func syncBundleDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open production bundle directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("sync production bundle directory"), syncErr, closeErr)
	}
	return nil
}
