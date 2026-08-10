// Package taeimage defines the content-addressed tag format used when TAE
// cannot accept an OCI digest reference directly.
package taeimage

import (
	"errors"
	"regexp"
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
	if len(reference) == 0 || len(reference) > 2048 {
		return "", errors.New("TAE sandbox image source length is invalid")
	}
	matches := digestReferencePattern.FindStringSubmatch(reference)
	if len(matches) != 3 {
		return "", errors.New("TAE sandbox image source must end in @sha256:<64 lowercase hex>")
	}
	return matches[1] + ":" + tagAlgorithm + matches[2], nil
}

// ValidateContentTag rejects ordinary mutable tags. TAE only receives tags
// whose name contains the complete manifest digest.
func ValidateContentTag(reference string) error {
	if len(reference) == 0 || len(reference) > 2048 || !contentTagPattern.MatchString(reference) {
		return errors.New("TAE sandbox image must end in :sha256-<64 lowercase hex>")
	}
	return nil
}
