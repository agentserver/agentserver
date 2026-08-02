package productionimage

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// VerifyImageTar proves that an exported container filesystem contains the
// exact manifest-defined file set, root ownership, modes, sizes, and bytes.
// Symlinks, hardlinks, devices, sockets, whiteouts, and undeclared entries are
// all release failures.
func VerifyImageTar(reader io.Reader, manifestBytes []byte) error {
	if reader == nil {
		return errors.New("production image tar reader is required")
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return err
	}
	directories := make(map[string]DirectoryEntry, len(manifest.Directories))
	for _, entry := range manifest.Directories {
		directories[entry.Path] = entry
	}
	files := fileMap(manifest.Files)
	files[ManifestPath] = FileEntry{
		Path: ManifestPath, SHA256: sha256Hex(manifestBytes), SizeBytes: int64(len(manifestBytes)), Mode: 0o444,
	}
	return verifyTarEntries(reader, directories, files)
}

func verifyTarEntries(reader io.Reader, directories map[string]DirectoryEntry, files map[string]FileEntry) error {
	archive := tar.NewReader(reader)
	seenDirectories := make(map[string]struct{}, len(directories))
	seenFiles := make(map[string]struct{}, len(files))
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read production image tar: %w", err)
		}
		name, isRoot, err := normalizeTarPath(header.Name)
		if err != nil {
			return err
		}
		if isRoot {
			if header.Typeflag != tar.TypeDir || header.Uid != 0 || header.Gid != 0 || uint32(header.Mode&0o7777) != 0o755 {
				return errors.New("production image tar root has invalid type, ownership, or mode")
			}
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			wanted, found := directories[name]
			if !found {
				return fmt.Errorf("production image tar contains undeclared directory %s", name)
			}
			if _, duplicate := seenDirectories[name]; duplicate {
				return fmt.Errorf("production image tar repeats directory %s", name)
			}
			if header.Size != 0 || header.Uid != int(wanted.UID) || header.Gid != int(wanted.GID) ||
				uint32(header.Mode&0o7777) != wanted.Mode {
				return fmt.Errorf("production image directory %s has invalid size, ownership, or mode", name)
			}
			seenDirectories[name] = struct{}{}
		case tar.TypeReg, tar.TypeRegA:
			wanted, found := files[name]
			if !found {
				return fmt.Errorf("production image tar contains undeclared file %s", name)
			}
			if _, duplicate := seenFiles[name]; duplicate {
				return fmt.Errorf("production image tar repeats file %s", name)
			}
			if header.Size != wanted.SizeBytes || header.Uid != int(wanted.UID) || header.Gid != int(wanted.GID) ||
				uint32(header.Mode&0o7777) != wanted.Mode {
				return fmt.Errorf("production image file %s has invalid size, ownership, or mode", name)
			}
			hasher := sha256.New()
			read, err := io.Copy(hasher, archive)
			if err != nil || read != wanted.SizeBytes {
				return errors.Join(fmt.Errorf("read production image file %s: got %d of %d bytes", name, read, wanted.SizeBytes), err)
			}
			if actual := hex.EncodeToString(hasher.Sum(nil)); actual != wanted.SHA256 {
				return fmt.Errorf("production image file %s SHA-256 = %s, want %s", name, actual, wanted.SHA256)
			}
			seenFiles[name] = struct{}{}
		default:
			return fmt.Errorf("production image tar contains forbidden type %d at %s", header.Typeflag, name)
		}
	}
	missingDirectories := missingEntryNames(directories, seenDirectories)
	missingFiles := missingEntryNames(files, seenFiles)
	if len(missingDirectories) != 0 || len(missingFiles) != 0 {
		return fmt.Errorf(
			"production image tar is incomplete: directories=%s files=%s",
			strings.Join(missingDirectories, ","), strings.Join(missingFiles, ","),
		)
	}
	return nil
}

func normalizeTarPath(value string) (string, bool, error) {
	if value == "." || value == "./" {
		return "", true, nil
	}
	if strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	value = strings.TrimSuffix(value, "/")
	if value == "" || strings.HasPrefix(value, "/") || path.Clean(value) != value || !fs.ValidPath(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", false, fmt.Errorf("production image tar contains non-canonical path %q", value)
	}
	return value, false, nil
}

func missingEntryNames[T any](wanted map[string]T, seen map[string]struct{}) []string {
	missing := make([]string, 0)
	for name := range wanted {
		if _, found := seen[name]; !found {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}
