package taeimage

import (
	"strings"
	"testing"
)

func TestContentTagForDigestReference(t *testing.T) {
	digest := strings.Repeat("a", 64)
	want := "registry.example:5443/team/sandbox:sha256-" + digest
	got, err := ContentTagForDigestReference("registry.example:5443/team/sandbox@sha256:" + digest)
	if err != nil || got != want {
		t.Fatalf("ContentTagForDigestReference() = %q, %v; want %q", got, err, want)
	}
	if err := ValidateContentTag(got); err != nil {
		t.Fatalf("ValidateContentTag() rejected derived tag: %v", err)
	}
}

func TestContentTagRejectsMutableOrMalformedReferences(t *testing.T) {
	digest := strings.Repeat("b", 64)
	for _, reference := range []string{
		"registry.example/sandbox:latest",
		"registry.example/sandbox:sha256-" + strings.Repeat("B", 64),
		"registry.example/sandbox@sha256:" + digest,
		"registry.example/sandbox:sha256-" + digest + "@sha256:" + digest,
	} {
		if err := ValidateContentTag(reference); err == nil {
			t.Fatalf("ValidateContentTag(%q) accepted invalid reference", reference)
		}
	}
	for _, reference := range []string{
		"registry.example/sandbox:latest",
		"registry.example/sandbox@sha256:" + strings.Repeat("B", 64),
		"registry.example/sandbox@sha256:" + digest[:63],
	} {
		if _, err := ContentTagForDigestReference(reference); err == nil {
			t.Fatalf("ContentTagForDigestReference(%q) accepted invalid reference", reference)
		}
	}
}
