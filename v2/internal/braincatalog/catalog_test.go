package braincatalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCanonicalRoundTrip(t *testing.T) {
	catalog, err := BuildCatalog("executor", "Deterministic executor tools.", []ToolDescriptor{{
		Name: "read_file", Description: "Read one file.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonical(catalog.CanonicalBytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Digest() != catalog.Digest() || !bytes.Equal(parsed.CanonicalBytes(), catalog.CanonicalBytes()) {
		t.Fatalf("parsed catalog differs: %s/%s", parsed.Digest(), catalog.Digest())
	}
}

func TestParseCanonicalRejectsNonCanonicalUnknownAndUnsortedCatalogs(t *testing.T) {
	catalog, err := BuildCatalog("executor", "tools", []ToolDescriptor{
		{Name: "a", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "b", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	nonCanonical := append([]byte(" \n"), catalog.CanonicalBytes()...)
	if _, err := ParseCanonical(nonCanonical, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "not RFC 8785 canonical") {
		t.Fatalf("non-canonical error = %v", err)
	}
	unknown := bytes.Replace(catalog.CanonicalBytes(), []byte(`"tools":`), []byte(`"policy":true,"tools":`), 1)
	if _, err := ParseCanonical(unknown, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "exactly fields") {
		t.Fatalf("unknown-field error = %v", err)
	}
	unsorted := bytes.Replace(catalog.CanonicalBytes(), []byte(`"name":"a"`), []byte(`"name":"z"`), 1)
	if _, err := ParseCanonical(unsorted, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "deterministic name order") {
		t.Fatalf("unsorted error = %v", err)
	}
}
