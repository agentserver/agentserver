package productionimage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func verifyPreparedPayload(root string, manifestBytes []byte, manifest Manifest) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o555 {
		return errors.Join(errors.New("prepared rootfs has the wrong type or mode"), err)
	}
	wantDirectories := make(map[string]DirectoryEntry, len(manifest.Directories))
	for _, directory := range manifest.Directories {
		wantDirectories[directory.Path] = directory
	}
	wantFiles := fileMap(manifest.Files)
	delete(wantFiles, CABundlePath)
	wantFiles[ManifestPath] = FileEntry{
		Path: ManifestPath, SHA256: sha256Hex(manifestBytes), SizeBytes: int64(len(manifestBytes)), Mode: 0o444,
	}
	seenDirectories := make([]string, 0, len(wantDirectories))
	seenFiles := make([]string, 0, len(wantFiles))
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !fs.ValidPath(relative) || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("prepared rootfs contains invalid entry %q", relative)
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			wanted, found := wantDirectories[relative]
			if !found || entryInfo.Mode().Perm() != os.FileMode(wanted.Mode) {
				return fmt.Errorf("prepared rootfs contains unexpected directory or mode %s", relative)
			}
			seenDirectories = append(seenDirectories, relative)
			return nil
		}
		wanted, found := wantFiles[relative]
		if !found || !entryInfo.Mode().IsRegular() || entryInfo.Mode().Perm() != os.FileMode(wanted.Mode) || entryInfo.Size() != wanted.SizeBytes {
			return fmt.Errorf("prepared rootfs contains unexpected file, type, mode, or size %s", relative)
		}
		if relative == ManifestPath {
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, manifestBytes) {
				return errors.Join(errors.New("prepared rootfs image manifest bytes differ"), err)
			}
		} else {
			digest, size, err := runtimelock.HashFile(path)
			if err != nil || digest != wanted.SHA256 || size != wanted.SizeBytes {
				return errors.Join(fmt.Errorf("prepared rootfs payload %s differs", relative), err)
			}
		}
		seenFiles = append(seenFiles, relative)
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(seenDirectories)
	slices.Sort(seenFiles)
	directoryPaths := make([]string, 0, len(wantDirectories))
	for path := range wantDirectories {
		directoryPaths = append(directoryPaths, path)
	}
	filePaths := make([]string, 0, len(wantFiles))
	for path := range wantFiles {
		filePaths = append(filePaths, path)
	}
	slices.Sort(directoryPaths)
	slices.Sort(filePaths)
	if !slices.Equal(seenDirectories, directoryPaths) || !slices.Equal(seenFiles, filePaths) {
		return fmt.Errorf(
			"prepared rootfs entry set differs: directories=%s files=%s",
			strings.Join(seenDirectories, ","), strings.Join(seenFiles, ","),
		)
	}
	return nil
}
