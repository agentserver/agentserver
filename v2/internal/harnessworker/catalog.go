// Package harnessworker contains the deterministic protocol core used by the
// per-run harness worker. It does not run a model or execute local tools.
package harnessworker

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/ucarion/jcs"
)

const (
	CatalogCanonicalizer = "rfc8785-v1"

	toolDigestDomain    = "agentserver-v2/brain-tool/rfc8785-v1\x00"
	catalogDigestDomain = "agentserver-v2/brain-tool-catalog/rfc8785-v1\x00"

	maxConfiguredTools            = 4 * 1024
	maxConfiguredNameBytes        = 1024
	maxConfiguredDescriptionBytes = 1024 * 1024
	maxConfiguredSchemaBytes      = 16 * 1024 * 1024
	maxConfiguredPayloadBytes     = 64 * 1024 * 1024
	maxConfiguredResultItems      = 4 * 1024
	maxConfiguredJSONValues       = 1024 * 1024
	maxConfiguredJSONDepth        = 256
)

// Limits bounds every model- or MCP-controlled catalog and call value before
// it is retained or forwarded. Zero values are invalid; use DefaultLimits.
type Limits struct {
	MaxTools            int
	MaxNameBytes        int
	MaxDescriptionBytes int
	MaxSchemaBytes      int
	MaxCatalogBytes     int
	MaxArgumentBytes    int
	MaxResultBytes      int
	MaxResultItems      int
	MaxResultTextBytes  int
	MaxJSONValues       int
	MaxJSONDepth        int
}

func DefaultLimits() Limits {
	return Limits{
		MaxTools:            64,
		MaxNameBytes:        128,
		MaxDescriptionBytes: 16 * 1024,
		MaxSchemaBytes:      256 * 1024,
		MaxCatalogBytes:     1024 * 1024,
		MaxArgumentBytes:    1024 * 1024,
		MaxResultBytes:      2 * 1024 * 1024,
		MaxResultItems:      64,
		MaxResultTextBytes:  2 * 1024 * 1024,
		MaxJSONValues:       65_536,
		MaxJSONDepth:        64,
	}
}

func (l Limits) validate() error {
	values := []struct {
		name  string
		value int
		max   int
	}{
		{"MaxTools", l.MaxTools, maxConfiguredTools},
		{"MaxNameBytes", l.MaxNameBytes, maxConfiguredNameBytes},
		{"MaxDescriptionBytes", l.MaxDescriptionBytes, maxConfiguredDescriptionBytes},
		{"MaxSchemaBytes", l.MaxSchemaBytes, maxConfiguredSchemaBytes},
		{"MaxCatalogBytes", l.MaxCatalogBytes, maxConfiguredPayloadBytes},
		{"MaxArgumentBytes", l.MaxArgumentBytes, maxConfiguredPayloadBytes},
		{"MaxResultBytes", l.MaxResultBytes, maxConfiguredPayloadBytes},
		{"MaxResultItems", l.MaxResultItems, maxConfiguredResultItems},
		{"MaxResultTextBytes", l.MaxResultTextBytes, maxConfiguredPayloadBytes},
		{"MaxJSONValues", l.MaxJSONValues, maxConfiguredJSONValues},
		{"MaxJSONDepth", l.MaxJSONDepth, maxConfiguredJSONDepth},
	}
	for _, field := range values {
		if field.value < 1 {
			return fmt.Errorf("harness worker limit %s must be positive", field.name)
		}
		if field.value > field.max {
			return fmt.Errorf("harness worker limit %s exceeds hard maximum %d", field.name, field.max)
		}
	}
	if l.MaxSchemaBytes > l.MaxCatalogBytes {
		return errors.New("harness worker MaxSchemaBytes cannot exceed MaxCatalogBytes")
	}
	if l.MaxResultTextBytes > l.MaxResultBytes {
		return errors.New("harness worker MaxResultTextBytes cannot exceed MaxResultBytes")
	}
	return nil
}

// ToolDescriptor is the trusted projection of one tools/list entry. MCP
// annotations and output schema deliberately do not enter the model catalog.
type ToolDescriptor struct {
	Name        string
	Description string
	InputSchema any
}

// CatalogTool is one immutable, canonicalized tool descriptor.
type CatalogTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Digest      string
}

type compiledTool struct {
	public   CatalogTool
	resolved *jsonschema.Resolved
}

// Catalog is the immutable tool surface for one brain thread.
type Catalog struct {
	namespace            string
	namespaceDescription string
	tools                []compiledTool
	byName               map[string]int
	canonical            []byte
	digest               string
	limits               Limits
}

// DynamicNamespace and DynamicFunction match the stock app-server
// thread/start.dynamicTools shape pinned by the Codex conformance fixture.
type DynamicNamespace struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tools       []DynamicFunction `json:"tools"`
}

type DynamicFunction struct {
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	DeferLoading bool            `json:"deferLoading"`
	InputSchema  json.RawMessage `json:"inputSchema"`
}

// BuildCatalog validates and canonicalizes an executor tools/list result. Tool
// order from the server is not significant; the frozen catalog is sorted by
// exact MCP tool name before hashing and projection.
func BuildCatalog(namespace, namespaceDescription string, descriptors []ToolDescriptor, limits Limits) (*Catalog, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if err := validateNamespace(namespace, limits.MaxNameBytes); err != nil {
		return nil, err
	}
	if err := validateText("namespace description", namespaceDescription, limits.MaxDescriptionBytes); err != nil {
		return nil, err
	}
	if len(descriptors) > limits.MaxTools {
		return nil, fmt.Errorf("tool catalog contains %d tools, limit is %d", len(descriptors), limits.MaxTools)
	}

	sorted := append([]ToolDescriptor(nil), descriptors...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	tools := make([]compiledTool, 0, len(sorted))
	byName := make(map[string]int, len(sorted))
	canonicalTools := make([]any, 0, len(sorted))
	for index, descriptor := range sorted {
		if err := validateToolName(descriptor.Name, namespace, limits.MaxNameBytes); err != nil {
			return nil, fmt.Errorf("tool %d: %w", index, err)
		}
		if _, duplicate := byName[descriptor.Name]; duplicate {
			return nil, fmt.Errorf("duplicate tool name %q", descriptor.Name)
		}
		if err := validateText("tool description", descriptor.Description, limits.MaxDescriptionBytes); err != nil {
			return nil, fmt.Errorf("tool %q: %w", descriptor.Name, err)
		}

		rawSchema, err := json.Marshal(descriptor.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q input schema: marshal: %w", descriptor.Name, err)
		}
		schemaValue, schemaCanonical, err := decodeCanonicalJSON(rawSchema, limits.MaxSchemaBytes, limits)
		if err != nil {
			return nil, fmt.Errorf("tool %q input schema: %w", descriptor.Name, err)
		}
		schemaObject, ok := schemaValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool %q input schema root must be an object", descriptor.Name)
		}
		if got, ok := schemaObject["type"].(string); !ok || got != "object" {
			return nil, fmt.Errorf("tool %q input schema must declare root type object", descriptor.Name)
		}
		if err := validateSchemaKeywords(schemaObject, "$", 0, limits.MaxJSONDepth); err != nil {
			return nil, fmt.Errorf("tool %q input schema: %w", descriptor.Name, err)
		}

		var schema jsonschema.Schema
		if err := json.Unmarshal(schemaCanonical, &schema); err != nil {
			return nil, fmt.Errorf("tool %q input schema: decode: %w", descriptor.Name, err)
		}
		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			return nil, fmt.Errorf("tool %q input schema: resolve: %w", descriptor.Name, err)
		}
		canonicalTool := map[string]any{
			"description": descriptor.Description,
			"inputSchema": schemaValue,
			"name":        descriptor.Name,
		}
		toolBytes, err := jcs.Append(nil, canonicalTool)
		if err != nil {
			return nil, fmt.Errorf("tool %q: canonicalize descriptor: %w", descriptor.Name, err)
		}
		public := CatalogTool{
			Name:        descriptor.Name,
			Description: descriptor.Description,
			InputSchema: append(json.RawMessage(nil), schemaCanonical...),
			Digest:      domainDigest(toolDigestDomain, toolBytes),
		}
		byName[descriptor.Name] = len(tools)
		tools = append(tools, compiledTool{public: public, resolved: resolved})
		canonicalTools = append(canonicalTools, canonicalTool)
	}

	catalogValue := map[string]any{
		"canonicalizer":        CatalogCanonicalizer,
		"namespace":            namespace,
		"namespaceDescription": namespaceDescription,
		"tools":                canonicalTools,
	}
	canonical, err := jcs.Append(nil, catalogValue)
	if err != nil {
		return nil, fmt.Errorf("canonicalize tool catalog: %w", err)
	}
	if len(canonical) > limits.MaxCatalogBytes {
		return nil, fmt.Errorf("canonical tool catalog is %d bytes, limit is %d", len(canonical), limits.MaxCatalogBytes)
	}
	return &Catalog{
		namespace:            namespace,
		namespaceDescription: namespaceDescription,
		tools:                tools,
		byName:               byName,
		canonical:            canonical,
		digest:               domainDigest(catalogDigestDomain, canonical),
		limits:               limits,
	}, nil
}

func (c *Catalog) Namespace() string { return c.namespace }

func (c *Catalog) NamespaceDescription() string { return c.namespaceDescription }

func (c *Catalog) Digest() string { return c.digest }

func (c *Catalog) CanonicalBytes() []byte { return append([]byte(nil), c.canonical...) }

func (c *Catalog) Tools() []CatalogTool {
	result := make([]CatalogTool, len(c.tools))
	for index, tool := range c.tools {
		result[index] = tool.public
		result[index].InputSchema = append(json.RawMessage(nil), tool.public.InputSchema...)
	}
	return result
}

// DynamicTools returns an empty list for an empty catalog, otherwise one fixed
// namespace containing the exact frozen tool descriptors.
func (c *Catalog) DynamicTools() []DynamicNamespace {
	if len(c.tools) == 0 {
		return []DynamicNamespace{}
	}
	functions := make([]DynamicFunction, len(c.tools))
	for index, tool := range c.tools {
		functions[index] = DynamicFunction{
			Type:         "function",
			Name:         tool.public.Name,
			Description:  tool.public.Description,
			DeferLoading: false,
			InputSchema:  append(json.RawMessage(nil), tool.public.InputSchema...),
		}
	}
	return []DynamicNamespace{{
		Type:        "namespace",
		Name:        c.namespace,
		Description: c.namespaceDescription,
		Tools:       functions,
	}}
}

// VerifyFrozen checks both the signed digest and, when supplied, the exact
// canonical bytes. Digest comparison is constant-time after strict hex decode.
func (c *Catalog) VerifyFrozen(expectedDigest string, expectedCanonical []byte) error {
	if !equalDigest(c.digest, expectedDigest) {
		return fmt.Errorf("tool catalog digest mismatch: got %s, want %s", c.digest, expectedDigest)
	}
	if expectedCanonical != nil && !bytes.Equal(c.canonical, expectedCanonical) {
		return errors.New("tool catalog canonical bytes do not match signed manifest")
	}
	return nil
}

// ValidateCall verifies an app-server callback against the frozen namespace,
// tool, and JSON Schema. It returns RFC 8785 canonical arguments for tools/call
// without adding defaults or otherwise rewriting model-provided values.
func (c *Catalog) ValidateCall(namespace, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	if namespace != c.namespace {
		return nil, fmt.Errorf("dynamic tool namespace %q is not frozen namespace %q", namespace, c.namespace)
	}
	index, ok := c.byName[toolName]
	if !ok {
		return nil, fmt.Errorf("dynamic tool %q is not in frozen catalog", toolName)
	}
	value, canonical, err := decodeCanonicalJSON(arguments, c.limits.MaxArgumentBytes, c.limits)
	if err != nil {
		return nil, fmt.Errorf("dynamic tool %q arguments: %w", toolName, err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("dynamic tool %q arguments must be a JSON object", toolName)
	}
	if err := c.tools[index].resolved.Validate(value); err != nil {
		return nil, fmt.Errorf("dynamic tool %q arguments do not match frozen schema: %w", toolName, err)
	}
	return append(json.RawMessage(nil), canonical...), nil
}

func validateNamespace(name string, maxBytes int) error {
	if err := validateNameText("namespace", name, maxBytes); err != nil {
		return err
	}
	for index, r := range name {
		if !asciiAlphaNumeric(r) && r != '_' && r != '-' {
			return fmt.Errorf("namespace %q contains invalid rune %q at byte %d", name, r, index)
		}
	}
	return nil
}

func validateToolName(name, namespace string, maxBytes int) error {
	if err := validateNameText("tool name", name, maxBytes); err != nil {
		return err
	}
	for index, r := range name {
		if !asciiAlphaNumeric(r) && r != '_' && r != '-' && r != '.' {
			return fmt.Errorf("tool name %q contains invalid rune %q at byte %d", name, r, index)
		}
	}
	qualifiedBytes := len(namespace) + 1 + len(name)
	if qualifiedBytes > maxBytes {
		return fmt.Errorf("qualified dynamic tool name %q exceeds %d bytes", namespace+"."+name, maxBytes)
	}
	return nil
}

func validateNameText(label, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	if err := validateText(label, value, maxBytes); err != nil {
		return err
	}
	return nil
}

func validateText(label, value string, maxBytes int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains NUL", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", label, len(value), maxBytes)
	}
	return nil
}

func asciiAlphaNumeric(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func domainDigest(domain string, value []byte) string {
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, domain)
	_, _ = hasher.Write(value)
	return hex.EncodeToString(hasher.Sum(nil))
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	if leftErr != nil || rightErr != nil || len(leftBytes) != sha256.Size || len(rightBytes) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

type jsonFrame struct {
	object    bool
	expectKey bool
	keys      map[string]struct{}
}

func decodeCanonicalJSON(raw []byte, maxBytes int, limits Limits) (any, []byte, error) {
	if len(raw) == 0 {
		return nil, nil, errors.New("JSON value is empty")
	}
	if len(raw) > maxBytes {
		return nil, nil, fmt.Errorf("JSON value is %d bytes, limit is %d", len(raw), maxBytes)
	}
	if !utf8.Valid(raw) {
		return nil, nil, errors.New("JSON value is not valid UTF-8")
	}
	if err := validateJSONTokens(raw, limits.MaxJSONValues, limits.MaxJSONDepth); err != nil {
		return nil, nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, fmt.Errorf("decode JSON: %w", err)
	}
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	if len(canonical) > maxBytes {
		return nil, nil, fmt.Errorf("canonical JSON is %d bytes, limit is %d", len(canonical), maxBytes)
	}
	return value, canonical, nil
}

func validateJSONTokens(raw []byte, maxValues, maxDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	frames := make([]jsonFrame, 0, 8)
	values := 0
	rootValues := 0
	completeValue := func() error {
		if len(frames) == 0 {
			rootValues++
			if rootValues > 1 {
				return errors.New("JSON contains more than one top-level value")
			}
			return nil
		}
		parent := &frames[len(frames)-1]
		if parent.object {
			if parent.expectKey {
				return errors.New("JSON object is missing a key")
			}
			parent.expectKey = true
		}
		return nil
	}

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode JSON token: %w", err)
		}
		values++
		if values > maxValues {
			return fmt.Errorf("JSON contains more than %d tokens", maxValues)
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				frames = append(frames, jsonFrame{object: true, expectKey: true, keys: make(map[string]struct{})})
			case '[':
				frames = append(frames, jsonFrame{})
			case '}', ']':
				if len(frames) == 0 {
					return errors.New("JSON has an unmatched closing delimiter")
				}
				frame := frames[len(frames)-1]
				if delimiter == '}' && (!frame.object || !frame.expectKey) {
					return errors.New("JSON object ended while expecting a value")
				}
				if delimiter == ']' && frame.object {
					return errors.New("JSON array ended with an object delimiter")
				}
				frames = frames[:len(frames)-1]
				if err := completeValue(); err != nil {
					return err
				}
			}
			if len(frames) > maxDepth {
				return fmt.Errorf("JSON nesting exceeds %d", maxDepth)
			}
			continue
		}

		if len(frames) > 0 {
			frame := &frames[len(frames)-1]
			if frame.object && frame.expectKey {
				key, ok := token.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, duplicate := frame.keys[key]; duplicate {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				frame.keys[key] = struct{}{}
				frame.expectKey = false
				continue
			}
		}
		if err := completeValue(); err != nil {
			return err
		}
	}
	if len(frames) != 0 {
		return errors.New("JSON ended with an open container")
	}
	if rootValues != 1 {
		return errors.New("JSON must contain exactly one top-level value")
	}
	return nil
}

var supportedSchemaKeywords = map[string]struct{}{
	"$id": {}, "$schema": {}, "$ref": {}, "$comment": {}, "$defs": {}, "definitions": {},
	"dependencies": {}, "$anchor": {}, "$dynamicAnchor": {}, "$dynamicRef": {}, "$vocabulary": {},
	"title": {}, "description": {}, "default": {}, "deprecated": {}, "readOnly": {}, "writeOnly": {}, "examples": {},
	"type": {}, "enum": {}, "const": {}, "multipleOf": {}, "minimum": {}, "maximum": {},
	"exclusiveMinimum": {}, "exclusiveMaximum": {}, "minLength": {}, "maxLength": {}, "pattern": {},
	"prefixItems": {}, "items": {}, "minItems": {}, "maxItems": {}, "additionalItems": {}, "uniqueItems": {},
	"contains": {}, "minContains": {}, "maxContains": {}, "unevaluatedItems": {},
	"minProperties": {}, "maxProperties": {}, "required": {}, "dependentRequired": {}, "properties": {},
	"patternProperties": {}, "additionalProperties": {}, "propertyNames": {}, "unevaluatedProperties": {},
	"allOf": {}, "anyOf": {}, "oneOf": {}, "not": {}, "if": {}, "then": {}, "else": {}, "dependentSchemas": {},
	"contentEncoding": {}, "contentMediaType": {}, "contentSchema": {}, "format": {},
}

func validateSchemaKeywords(schema map[string]any, path string, depth, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("schema nesting exceeds %d", maxDepth)
	}
	for keyword := range schema {
		if _, ok := supportedSchemaKeywords[keyword]; !ok {
			return fmt.Errorf("unsupported schema keyword %q at %s", keyword, path)
		}
	}

	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
		if value, exists := schema[keyword]; exists {
			entries, ok := value.(map[string]any)
			if !ok {
				continue
			}
			for name, child := range entries {
				if err := validateSchemaValue(child, path+"/"+keyword+"/"+name, depth+1, maxDepth); err != nil {
					return err
				}
			}
		}
	}
	if value, exists := schema["dependencies"]; exists {
		if entries, ok := value.(map[string]any); ok {
			for name, child := range entries {
				if _, isArray := child.([]any); isArray {
					continue
				}
				if err := validateSchemaValue(child, path+"/dependencies/"+name, depth+1, maxDepth); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{
		"items", "additionalItems", "contains", "unevaluatedItems", "additionalProperties", "propertyNames",
		"unevaluatedProperties", "not", "if", "then", "else", "contentSchema",
	} {
		if child, exists := schema[keyword]; exists {
			if err := validateSchemaValue(child, path+"/"+keyword, depth+1, maxDepth); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"prefixItems", "allOf", "anyOf", "oneOf"} {
		if value, exists := schema[keyword]; exists {
			children, ok := value.([]any)
			if !ok {
				continue
			}
			for index, child := range children {
				if err := validateSchemaValue(child, fmt.Sprintf("%s/%s/%d", path, keyword, index), depth+1, maxDepth); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSchemaValue(value any, path string, depth, maxDepth int) error {
	if boolean, ok := value.(bool); ok {
		_ = boolean
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("schema at %s must be an object or boolean", path)
	}
	return validateSchemaKeywords(object, path, depth, maxDepth)
}
