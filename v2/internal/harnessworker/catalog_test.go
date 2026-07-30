package harnessworker

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCatalogCanonicalizesOrderAndProjectsDynamicTools(t *testing.T) {
	limits := DefaultLimits()
	first, err := BuildCatalog("executor", "Deterministic executor tools.", []ToolDescriptor{
		{
			Name:        "write_file",
			Description: "Write one file.",
			InputSchema: json.RawMessage(`{"required":["path","text"],"properties":{"text":{"type":"string"},"path":{"type":"string"}},"additionalProperties":false,"type":"object"}`),
		},
		{
			Name:        "read_file",
			Description: "Read one file.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		},
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCatalog("executor", "Deterministic executor tools.", []ToolDescriptor{
		{
			Name:        "read_file",
			Description: "Read one file.",
			InputSchema: map[string]any{
				"additionalProperties": false,
				"required":             []any{"path"},
				"properties":           map[string]any{"path": map[string]any{"type": "string"}},
				"type":                 "object",
			},
		},
		{
			Name:        "write_file",
			Description: "Write one file.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"text":{"type":"string"}},"required":["path","text"]}`),
		},
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatalf("equivalent catalogs differ: %s != %s\n%s\n%s", first.Digest(), second.Digest(), first.CanonicalBytes(), second.CanonicalBytes())
	}
	tools := first.Tools()
	if len(tools) != 2 || tools[0].Name != "read_file" || tools[1].Name != "write_file" {
		t.Fatalf("catalog tools are not sorted: %+v", tools)
	}
	dynamic := first.DynamicTools()
	if len(dynamic) != 1 || dynamic[0].Type != "namespace" || dynamic[0].Name != "executor" || len(dynamic[0].Tools) != 2 {
		t.Fatalf("unexpected dynamic tool projection: %+v", dynamic)
	}
	if dynamic[0].Tools[0].Name != "read_file" || dynamic[0].Tools[0].DeferLoading {
		t.Fatalf("unexpected dynamic function projection: %+v", dynamic[0].Tools[0])
	}
	encoded, err := json.Marshal(dynamic)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"type":"namespace"`, `"type":"function"`, `"deferLoading":false`, `"inputSchema":{"additionalProperties":false`} {
		if !bytes.Contains(encoded, []byte(fragment)) {
			t.Fatalf("dynamic projection omitted %s: %s", fragment, encoded)
		}
	}
	if err := first.VerifyFrozen(first.Digest(), first.CanonicalBytes()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCatalogRejectsAmbiguousOrUnsafeDescriptors(t *testing.T) {
	valid := ToolDescriptor{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}
	tests := []struct {
		name        string
		namespace   string
		descriptors []ToolDescriptor
		limits      Limits
		want        string
	}{
		{"invalid namespace", "executor.tools", []ToolDescriptor{valid}, DefaultLimits(), "invalid rune"},
		{"invalid tool rune", "executor", []ToolDescriptor{{Name: "read/file", InputSchema: valid.InputSchema}}, DefaultLimits(), "invalid rune"},
		{"qualified name too long", "executor", []ToolDescriptor{{Name: strings.Repeat("a", 120), InputSchema: valid.InputSchema}}, DefaultLimits(), "qualified dynamic tool name"},
		{"duplicate name", "executor", []ToolDescriptor{valid, valid}, DefaultLimits(), "duplicate tool name"},
		{"non-object root", "executor", []ToolDescriptor{{Name: "bad", InputSchema: json.RawMessage(`[]`)}}, DefaultLimits(), "root must be an object"},
		{"missing object type", "executor", []ToolDescriptor{{Name: "bad", InputSchema: json.RawMessage(`{"properties":{}}`)}}, DefaultLimits(), "declare root type object"},
		{"duplicate JSON key", "executor", []ToolDescriptor{{Name: "bad", InputSchema: json.RawMessage(`{"type":"object","type":"array"}`)}}, DefaultLimits(), "duplicate JSON object key"},
		{"unknown schema keyword", "executor", []ToolDescriptor{{Name: "bad", InputSchema: json.RawMessage(`{"type":"object","x-untrusted":true}`)}}, DefaultLimits(), "unsupported schema keyword"},
		{"remote ref", "executor", []ToolDescriptor{{Name: "bad", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"https://example.invalid/schema"}}}`)}}, DefaultLimits(), "cannot resolve remote schemas"},
		{"tool bound", "executor", []ToolDescriptor{valid, {Name: "write_file", InputSchema: valid.InputSchema}}, func() Limits { l := DefaultLimits(); l.MaxTools = 1; return l }(), "limit is 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildCatalog(test.namespace, "tools", test.descriptors, test.limits)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildCatalog() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCatalogValidateCallUsesFrozenSchemaWithoutDefaults(t *testing.T) {
	catalog, err := BuildCatalog("executor", "tools", []ToolDescriptor{{
		Name:        "approved_echo",
		Description: "Echo a message.",
		InputSchema: json.RawMessage(`{
  "type":"object",
  "properties":{"message":{"type":"string"},"count":{"type":"integer","default":1}},
  "required":["message"],
  "additionalProperties":false
}`),
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := catalog.ValidateCall("executor", "approved_echo", json.RawMessage(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(arguments) != `{"message":"hello"}` {
		t.Fatalf("validated arguments were rewritten: %s", arguments)
	}
	tests := []struct {
		name      string
		namespace string
		tool      string
		arguments string
		want      string
	}{
		{"namespace", "other", "approved_echo", `{"message":"hello"}`, "not frozen namespace"},
		{"tool", "executor", "blocked", `{"message":"hello"}`, "not in frozen catalog"},
		{"required", "executor", "approved_echo", `{}`, "required"},
		{"additional", "executor", "approved_echo", `{"message":"hello","endpoint":"evil"}`, "additional properties"},
		{"type", "executor", "approved_echo", `{"message":4}`, `want "string"`},
		{"duplicate", "executor", "approved_echo", `{"message":"one","message":"two"}`, "duplicate JSON object key"},
		{"object", "executor", "approved_echo", `[]`, "must be a JSON object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := catalog.ValidateCall(test.namespace, test.tool, json.RawMessage(test.arguments))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ValidateCall() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCatalogDefensiveCopiesAndDigestVerification(t *testing.T) {
	catalog, err := BuildCatalog("executor", "tools", []ToolDescriptor{{
		Name:        "read_file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	canonical := catalog.CanonicalBytes()
	canonical[0] = 'x'
	tools := catalog.Tools()
	tools[0].InputSchema[0] = 'x'
	dynamic := catalog.DynamicTools()
	dynamic[0].Tools[0].InputSchema[0] = 'x'
	if catalog.CanonicalBytes()[0] != '{' || catalog.Tools()[0].InputSchema[0] != '{' || catalog.DynamicTools()[0].Tools[0].InputSchema[0] != '{' {
		t.Fatal("catalog exposed mutable internal bytes")
	}
	if err := catalog.VerifyFrozen(strings.Repeat("0", 64), catalog.CanonicalBytes()); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
	changed := catalog.CanonicalBytes()
	changed[len(changed)-1] = ' '
	if err := catalog.VerifyFrozen(catalog.Digest(), changed); err == nil || !strings.Contains(err.Error(), "canonical bytes") {
		t.Fatalf("canonical mismatch error = %v", err)
	}
}

func TestEmptyCatalogProducesNoDynamicNamespace(t *testing.T) {
	catalog, err := BuildCatalog("executor", "tools", nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if dynamic := catalog.DynamicTools(); dynamic == nil || len(dynamic) != 0 {
		t.Fatalf("empty dynamic tools = %#v, want non-nil empty list", dynamic)
	}
}

func TestLimitsRejectValuesAboveHardCeilings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Limits)
	}{
		{name: "payload bytes", mutate: func(limits *Limits) { limits.MaxCatalogBytes = maxConfiguredPayloadBytes + 1 }},
		{name: "tool count", mutate: func(limits *Limits) { limits.MaxTools = maxConfiguredTools + 1 }},
		{name: "JSON depth", mutate: func(limits *Limits) { limits.MaxJSONDepth = maxConfiguredJSONDepth + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.mutate(&limits)
			if err := limits.validate(); err == nil || !strings.Contains(err.Error(), "hard maximum") {
				t.Fatalf("Limits.validate() error = %v, want hard maximum rejection", err)
			}
		})
	}
}
