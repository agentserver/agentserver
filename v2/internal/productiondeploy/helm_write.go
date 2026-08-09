package productiondeploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var requiredHelmBaseChartFiles = []string{
	helmChartFile,
	helmValuesFile,
	helmValuesSchemaFile,
	helmHelpersFile,
	helmFoundationTemplateFile,
	helmHydraMigrationTemplateFile,
	helmMigrationTemplateFile,
	helmHydraSetupTemplateFile,
	helmBootstrapTemplateFile,
	helmTAENetworkProbeTemplateFile,
	helmRuntimeTemplateFile,
	helmFoundationManifestFile,
	helmHydraMigrationManifestFile,
	helmMigrationManifestFile,
	helmHydraSetupManifestFile,
	helmBootstrapManifestFile,
	helmTAENetworkProbeManifestFile,
	helmRuntimeManifestFile,
	helmConfigFile,
	helmChecksumsFile,
}

var requiredHelmManagedChartFiles = append(append([]string(nil), requiredHelmBaseChartFiles...),
	helmManagedEnvironmentTemplateFile, helmManagedEnvironmentManifestFile,
)

// WriteHelmChart atomically publishes an immutable chart directory. As with
// WriteBundle, an exact retry is accepted and a differing destination is never
// overwritten.
func WriteHelmChart(chart HelmChart, destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || filepath.Base(destination) == "." {
		return errors.New("production Helm destination must be an absolute clean child path")
	}
	if err := validateHelmChart(chart); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("production Helm parent must be a direct directory not writable by group or other")
	}
	if _, err := os.Lstat(destination); err == nil {
		return verifyHelmChartDirectory(chart, destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect production Helm destination: %w", err)
	}

	temporary, err := os.MkdirTemp(parent, ".agentserver-helm-")
	if err != nil {
		return fmt.Errorf("create production Helm staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	for _, rendered := range chart.Files {
		path := filepath.Join(temporary, filepath.FromSlash(rendered.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create production Helm directory for %s: %w", rendered.Name, err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create production Helm file %s: %w", rendered.Name, err)
		}
		written, writeErr := file.Write(rendered.Content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || written != len(rendered.Content) {
			return errors.Join(fmt.Errorf("write production Helm file %s", rendered.Name), writeErr, syncErr, closeErr)
		}
		if err := os.Chmod(path, bundleFileMode); err != nil {
			return fmt.Errorf("seal production Helm file %s: %w", rendered.Name, err)
		}
	}
	directories, err := chartDirectories(chart)
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		path := temporary
		if directories[index] != "." {
			path = filepath.Join(temporary, filepath.FromSlash(directories[index]))
		}
		if err := os.Chmod(path, bundleDirectoryMode); err != nil {
			return fmt.Errorf("seal production Helm directory %s: %w", directories[index], err)
		}
		if err := syncBundleDirectory(path); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, destination); err != nil {
		if _, inspectErr := os.Lstat(destination); inspectErr == nil && verifyHelmChartDirectory(chart, destination) == nil {
			return nil
		}
		return fmt.Errorf("publish production Helm chart: %w", err)
	}
	published = true
	if err := syncBundleDirectory(parent); err != nil {
		return err
	}
	return verifyHelmChartDirectory(chart, destination)
}

func validateHelmChart(chart HelmChart) error {
	actual := make([]string, 0, len(chart.Files))
	for _, file := range chart.Files {
		if !fs.ValidPath(file.Name) || strings.Contains(file.Name, "\\") || len(file.Content) == 0 || sha256Hex(file.Content) != file.SHA256 {
			return fmt.Errorf("production Helm chart contains invalid file %q", file.Name)
		}
		actual = append(actual, file.Name)
	}
	slices.Sort(actual)
	base := append([]string(nil), requiredHelmBaseChartFiles...)
	managed := append([]string(nil), requiredHelmManagedChartFiles...)
	slices.Sort(base)
	slices.Sort(managed)
	if !slices.Equal(actual, base) && !slices.Equal(actual, managed) {
		return errors.New("production Helm chart has an unexpected file set")
	}
	return nil
}

func chartDirectories(chart HelmChart) ([]string, error) {
	set := map[string]struct{}{".": {}}
	for _, file := range chart.Files {
		for directory := filepath.ToSlash(filepath.Dir(file.Name)); directory != "."; directory = filepath.ToSlash(filepath.Dir(directory)) {
			set[directory] = struct{}{}
		}
	}
	directories := make([]string, 0, len(set))
	for directory := range set {
		directories = append(directories, directory)
	}
	slices.SortFunc(directories, func(left, right string) int {
		leftDepth := strings.Count(left, "/")
		rightDepth := strings.Count(right, "/")
		if leftDepth != rightDepth {
			return leftDepth - rightDepth
		}
		return strings.Compare(left, right)
	})
	return directories, nil
}

func verifyHelmChartDirectory(chart HelmChart, root string) error {
	directories, err := chartDirectories(chart)
	if err != nil {
		return err
	}
	wantedFiles := make(map[string]RenderedFile, len(chart.Files))
	for _, file := range chart.Files {
		wantedFiles[file.Name] = file
	}
	wantedDirectories := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		wantedDirectories[directory] = struct{}{}
	}
	seenFiles := make(map[string]struct{}, len(wantedFiles))
	seenDirectories := make(map[string]struct{}, len(wantedDirectories))
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("production Helm path %s has the wrong type", relative)
		}
		if entry.IsDir() {
			if _, found := wantedDirectories[relative]; !found || info.Mode().Perm() != bundleDirectoryMode {
				return fmt.Errorf("production Helm directory %s is unexpected or has the wrong mode", relative)
			}
			seenDirectories[relative] = struct{}{}
			return nil
		}
		rendered, found := wantedFiles[relative]
		if !found || !info.Mode().IsRegular() || info.Mode().Perm() != bundleFileMode || info.Size() != int64(len(rendered.Content)) {
			return fmt.Errorf("production Helm file %s is unexpected or has the wrong mode or size", relative)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(rendered.Content))+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, rendered.Content) {
			return errors.Join(fmt.Errorf("production Helm file %s differs", relative), readErr, closeErr)
		}
		seenFiles[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seenFiles) != len(wantedFiles) || len(seenDirectories) != len(wantedDirectories) {
		return errors.New("existing production Helm chart is incomplete")
	}
	return nil
}
