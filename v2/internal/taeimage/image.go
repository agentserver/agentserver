// Package taeimage defines the content-addressed tag format used when TAE
// cannot accept an OCI digest reference directly.
package taeimage

import (
	"errors"
	"regexp"
	"strings"
)

const tagAlgorithm = "sha256-"

var (
	digestReferencePattern = regexp.MustCompile(`^([^[:space:]@]+)@sha256:([0-9a-f]{64})$`)
	contentTagPattern      = regexp.MustCompile(`^[^[:space:]@]+:sha256-[0-9a-f]{64}$`)
)

// ContentTagForDigestReference converts an immutable OCI digest reference to
// the deterministic tag accepted by TAE. The complete digest remains encoded
// in the tag; release publication must verify that the tag resolves to the
// same manifest digest before promotion.
func ContentTagForDigestReference(reference string) (string, error) {
	matches := digestReferencePattern.FindStringSubmatch(reference)
	if len(matches) != 3 {
		return "", errors.New("TAE sandbox image source must end in @sha256:<64 lowercase hex>")
	}
	return contentTag(matches[1], matches[2])
}

// ContentTagForRepository mirrors the digest identity from sourceReference
// into a different registry repository. This is used for TAE's region-local
// ICM repository while production.json keeps the independently verified SG
// release mirror as its supply-chain lock.
func ContentTagForRepository(repository, sourceReference string) (string, error) {
	matches := digestReferencePattern.FindStringSubmatch(sourceReference)
	if len(matches) != 3 {
		return "", errors.New("TAE sandbox image source must end in @sha256:<64 lowercase hex>")
	}
	return contentTag(repository, matches[2])
}

// ValidateContentTag rejects ordinary mutable tags. TAE only receives tags
// whose name contains the complete manifest digest.
func ValidateContentTag(reference string) error {
	if len(reference) == 0 || len(reference) > 2048 || !contentTagPattern.MatchString(reference) {
		return errors.New("TAE sandbox image must end in :sha256-<64 lowercase hex>")
	}
	return nil
}

func contentTag(repository, digest string) (string, error) {
	if len(repository) == 0 || len(repository)+1+len(tagAlgorithm)+len(digest) > 2048 ||
		strings.TrimSpace(repository) != repository || strings.ContainsAny(repository, "@\x00\r\n\t ") ||
		!strings.Contains(repository, "/") || strings.HasSuffix(repository, "/") {
		return "", errors.New("TAE sandbox image repository is invalid")
	}
	lastComponent := repository[strings.LastIndex(repository, "/")+1:]
	if strings.Contains(lastComponent, ":") {
		return "", errors.New("TAE sandbox image repository must not include a tag")
	}
	return repository + ":" + tagAlgorithm + digest, nil
}
