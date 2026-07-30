package coredb

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAndHashCanonicalJSONValidatesBeforeDomainSeparatedHash(t *testing.T) {
	objectValidator := func(value any) error {
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("want object")
		}
		if _, ok := object["path"].(string); !ok {
			return errors.New("path is required")
		}
		return nil
	}
	firstCanonical, first, err := ValidateAndHashCanonicalJSON(
		HashDomainExecutionArguments,
		[]byte(`{"path":"/tmp/a","count":1}`),
		objectValidator,
	)
	if err != nil {
		t.Fatalf("ValidateAndHashCanonicalJSON() error = %v", err)
	}
	secondCanonical, second, err := ValidateAndHashCanonicalJSON(
		HashDomainExecutionArguments,
		[]byte("{ \"count\" : 1, \"path\" : \"/tmp/a\" }"),
		objectValidator,
	)
	if err != nil {
		t.Fatalf("second ValidateAndHashCanonicalJSON() error = %v", err)
	}
	if string(firstCanonical) != `{"count":1,"path":"/tmp/a"}` || string(secondCanonical) != string(firstCanonical) {
		t.Fatalf("canonical values = %s and %s", firstCanonical, secondCanonical)
	}
	if !first.equal(second) || first.CanonicalizerVersion() != CanonicalizerRFC8785V1 {
		t.Fatalf("equivalent canonical hashes differ: %x != %x", first.SHA256(), second.SHA256())
	}

	_, otherDomain, err := ValidateAndHashCanonicalJSON(HashDomainPolicyContext, firstCanonical, objectValidator)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256() == otherDomain.SHA256() {
		t.Fatal("different semantic domains produced the same digest")
	}
}

func TestValidateAndHashCanonicalJSONRejectsUnvalidatedOrAmbiguousInput(t *testing.T) {
	tests := []struct {
		name      string
		raw       []byte
		validator JSONValueValidator
		want      string
	}{
		{name: "validator required", raw: []byte(`{}`), want: "validator is required"},
		{name: "schema rejection", raw: []byte(`[]`), validator: func(any) error { return errors.New("object required") }, want: "does not match its schema"},
		{name: "duplicate key", raw: []byte(`{"x":1,"x":2}`), validator: func(any) error { return nil }, want: "duplicate"},
		{name: "multiple roots", raw: []byte(`{} {}`), validator: func(any) error { return nil }, want: "more than one"},
		{name: "unpaired high surrogate", raw: []byte(`{"x":"\ud800"}`), validator: func(any) error { return nil }, want: "unpaired high surrogate"},
		{name: "unpaired low surrogate", raw: []byte(`{"x":"\udc00"}`), validator: func(any) error { return nil }, want: "unpaired low surrogate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ValidateAndHashCanonicalJSON(HashDomainOperationPlan, test.raw, test.validator)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAndHashCanonicalJSON() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateAndHashCanonicalJSONAcceptsPairedSurrogatesAndEscapedBackslash(t *testing.T) {
	canonical, _, err := ValidateAndHashCanonicalJSON(
		HashDomainOperationResult,
		[]byte(`{"emoji":"\ud83d\ude00","literal":"\\ud800"}`),
		func(any) error { return nil },
	)
	if err != nil {
		t.Fatalf("ValidateAndHashCanonicalJSON() error = %v", err)
	}
	if string(canonical) != `{"emoji":"😀","literal":"\\ud800"}` {
		t.Fatalf("canonical JSON = %s", canonical)
	}
}
