// Package buildguard contains repository-boundary checks used by v2 CI.
package buildguard

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const v1RuntimeImportPrefix = "github.com/agentserver/agentserver/internal"

// ImportViolation identifies a v2 source file that imports the v1 runtime.
type ImportViolation struct {
	File       string
	ImportPath string
}

func (v ImportViolation) String() string {
	return fmt.Sprintf("%s imports %s", v.File, v.ImportPath)
}

// FindV1RuntimeImports scans Go source beneath root without resolving modules.
// Parsing source directly keeps the guard useful even when a forbidden import
// would make `go list` fail before CI could explain the architecture violation.
func FindV1RuntimeImports(root string) ([]ImportViolation, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve scan root: %w", err)
	}

	var violations []ImportViolation
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && shouldSkipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", path, err)
			}
			if importPath != v1RuntimeImportPrefix && !strings.HasPrefix(importPath, v1RuntimeImportPrefix+"/") {
				continue
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("make path relative: %w", err)
			}
			violations = append(violations, ImportViolation{
				File:       filepath.ToSlash(relative),
				ImportPath: importPath,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File == violations[j].File {
			return violations[i].ImportPath < violations[j].ImportPath
		}
		return violations[i].File < violations[j].File
	})
	return violations, nil
}

func shouldSkipDirectory(name string) bool {
	switch name {
	case ".git", ".worktrees", "node_modules", "vendor":
		return true
	default:
		return false
	}
}
