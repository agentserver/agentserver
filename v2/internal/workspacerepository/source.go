// Package workspacerepository defines the repository identity independently
// from executor host paths and the harness's isolated local cwd.
package workspacerepository

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const GitHost = "code.byted.org"

var (
	repositorySegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	refPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)
	bindingPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type Source struct {
	URL                 string `json:"url"`
	Ref                 string `json:"ref"`
	WorkingDirectory    string `json:"workingDirectory"`
	CredentialBindingID string `json:"credentialBindingId,omitempty"`
}

func NormalizeRepositoryURL(raw string) (string, error) {
	invalid := errors.New("repository must be an HTTPS Codebase repository URL without credentials, port, query, fragment or encoded path")
	if raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != GitHost || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return "", invalid
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segments) != 2 {
		return "", invalid
	}
	for _, segment := range segments {
		if !repositorySegment.MatchString(segment) || segment == "." || segment == ".." || strings.HasSuffix(segment, ".") {
			return "", invalid
		}
	}
	repo := strings.TrimSuffix(segments[1], ".git")
	if repo == "" {
		return "", invalid
	}
	u.Path = "/" + segments[0] + "/" + repo + ".git"
	return u.String(), nil
}

func (source Source) Validate() error {
	canonical, err := NormalizeRepositoryURL(source.URL)
	if err != nil {
		return err
	}
	if canonical != source.URL {
		return errors.New("repository URL must be canonical")
	}
	if source.Ref != "" {
		if !refPattern.MatchString(source.Ref) || strings.Contains(source.Ref, "..") || strings.Contains(source.Ref, "//") || strings.HasSuffix(source.Ref, "/") {
			return errors.New("repository ref is not a bounded branch, tag or commit identifier")
		}
		for _, segment := range strings.Split(source.Ref, "/") {
			if strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, ".lock") {
				return errors.New("repository ref has an invalid segment")
			}
		}
	}
	if err := validateRepositoryDirectory(source.WorkingDirectory); err != nil {
		return err
	}
	if strings.Contains(source.WorkingDirectory, ":") {
		return errors.New("working directory must be a relative repository path, not a URL")
	}
	if source.CredentialBindingID != "" && (!bindingPattern.MatchString(source.CredentialBindingID) || source.CredentialBindingID == "00000000-0000-0000-0000-000000000000") {
		return errors.New("Git credential binding ID is invalid")
	}
	return nil
}

func validateRepositoryDirectory(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return errors.New("working directory is invalid")
	}
	if value == "." {
		return nil
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || path.Clean(value) != value {
		return errors.New("working directory must be a clean relative path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("working directory contains an invalid segment")
		}
		for _, r := range part {
			if r == 0 || unicode.IsControl(r) {
				return errors.New("working directory contains a control character")
			}
		}
	}
	return nil
}
