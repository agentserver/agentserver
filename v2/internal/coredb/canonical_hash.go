package coredb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/ucarion/jcs"
)

const (
	CanonicalizerRFC8785V1     = "rfc8785-v1"
	MaxCanonicalHashJSONBytes  = 1024 * 1024
	maxCanonicalHashJSONDepth  = 64
	maxCanonicalHashJSONTokens = 65_536
)

// CanonicalHashDomain prevents a digest created for one semantic input from
// being substituted for a different input that happens to have the same JSON.
type CanonicalHashDomain string

const (
	HashDomainExecutionArguments CanonicalHashDomain = "execution-arguments"
	HashDomainToolSchema         CanonicalHashDomain = "tool-schema"
	HashDomainOperationPlan      CanonicalHashDomain = "operation-plan"
	HashDomainPolicyContext      CanonicalHashDomain = "policy-context"
	HashDomainOperationParams    CanonicalHashDomain = "operation-params"
	HashDomainOperationAck       CanonicalHashDomain = "operation-ack"
	HashDomainOperationResult    CanonicalHashDomain = "operation-result"
	HashDomainExecutionResult    CanonicalHashDomain = "execution-result"
)

// CanonicalJSONHash can only be constructed by ValidateAndHashCanonicalJSON
// or by the database scanner in this package. Command validation checks the
// expected domain before a digest is persisted or compared.
type CanonicalJSONHash struct {
	domain        CanonicalHashDomain
	digest        [sha256.Size]byte
	canonicalizer string
}

func (h CanonicalJSONHash) Domain() CanonicalHashDomain { return h.domain }

func (h CanonicalJSONHash) SHA256() [sha256.Size]byte { return h.digest }

func (h CanonicalJSONHash) CanonicalizerVersion() string { return h.canonicalizer }

func (h CanonicalJSONHash) equal(other CanonicalJSONHash) bool {
	return h.domain == other.domain && h.canonicalizer == other.canonicalizer && h.digest == other.digest
}

// JSONValueValidator is the schema-validation boundary for a hash input. The
// validator is deliberately required: canonicalization without validating the
// value against its versioned schema is not a valid execution fingerprint.
type JSONValueValidator func(any) error

// ValidateAndHashCanonicalJSON rejects malformed or duplicate-key JSON,
// validates the decoded value, canonicalizes it with RFC 8785 JCS, and only
// then hashes the domain-separated canonical bytes. The returned bytes are a
// defensive copy suitable for encryption or an independent plan comparison.
func ValidateAndHashCanonicalJSON(
	domain CanonicalHashDomain,
	raw json.RawMessage,
	validator JSONValueValidator,
) (json.RawMessage, CanonicalJSONHash, error) {
	if !validCanonicalHashDomain(domain) {
		return nil, CanonicalJSONHash{}, fmt.Errorf("unknown canonical hash domain %q", domain)
	}
	if validator == nil {
		return nil, CanonicalJSONHash{}, errors.New("canonical hash schema validator is required")
	}
	if len(raw) == 0 {
		return nil, CanonicalJSONHash{}, errors.New("canonical hash JSON is empty")
	}
	if len(raw) > MaxCanonicalHashJSONBytes {
		return nil, CanonicalJSONHash{}, fmt.Errorf("canonical hash JSON exceeds %d bytes", MaxCanonicalHashJSONBytes)
	}
	if !utf8.Valid(raw) {
		return nil, CanonicalJSONHash{}, errors.New("canonical hash JSON is not valid UTF-8")
	}
	if err := validateCanonicalHashJSONStringEscapes(raw); err != nil {
		return nil, CanonicalJSONHash{}, err
	}
	if err := validateCanonicalHashJSONTokens(raw); err != nil {
		return nil, CanonicalJSONHash{}, err
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return nil, CanonicalJSONHash{}, fmt.Errorf("decode canonical hash JSON: %w", err)
	}
	if err := validator(value); err != nil {
		return nil, CanonicalJSONHash{}, fmt.Errorf("canonical hash JSON does not match its schema: %w", err)
	}
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		return nil, CanonicalJSONHash{}, fmt.Errorf("canonicalize hash JSON: %w", err)
	}
	if len(canonical) > MaxCanonicalHashJSONBytes {
		return nil, CanonicalJSONHash{}, fmt.Errorf("canonical hash JSON exceeds %d bytes after canonicalization", MaxCanonicalHashJSONBytes)
	}

	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "agentserver-v2/"+string(domain)+"/"+CanonicalizerRFC8785V1+"\x00")
	_, _ = hasher.Write(canonical)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return append(json.RawMessage(nil), canonical...), CanonicalJSONHash{
		domain:        domain,
		digest:        digest,
		canonicalizer: CanonicalizerRFC8785V1,
	}, nil
}

func validateCanonicalHashJSONStringEscapes(raw []byte) error {
	inString := false
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			index++
			if index >= len(raw) {
				return errors.New("canonical hash JSON string ends with an escape")
			}
			if raw[index] != 'u' {
				continue
			}
			value, ok := decodeJSONHexQuad(raw, index+1)
			if !ok {
				return errors.New("canonical hash JSON contains an invalid Unicode escape")
			}
			index += 4
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
					return errors.New("canonical hash JSON contains an unpaired high surrogate")
				}
				low, ok := decodeJSONHexQuad(raw, index+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errors.New("canonical hash JSON contains an unpaired high surrogate")
				}
				index += 6
			case value >= 0xdc00 && value <= 0xdfff:
				return errors.New("canonical hash JSON contains an unpaired low surrogate")
			}
		}
	}
	return nil
}

func decodeJSONHexQuad(raw []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for _, character := range raw[start : start+4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func validCanonicalHashDomain(domain CanonicalHashDomain) bool {
	switch domain {
	case HashDomainExecutionArguments,
		HashDomainToolSchema,
		HashDomainOperationPlan,
		HashDomainPolicyContext,
		HashDomainOperationParams,
		HashDomainOperationAck,
		HashDomainOperationResult,
		HashDomainExecutionResult:
		return true
	default:
		return false
	}
}

func validateCanonicalHash(field string, hash CanonicalJSONHash, domain CanonicalHashDomain) error {
	if hash.domain != domain {
		return fmt.Errorf("%s must use canonical hash domain %q", field, domain)
	}
	if hash.canonicalizer != CanonicalizerRFC8785V1 {
		return fmt.Errorf("%s must use canonicalizer %q", field, CanonicalizerRFC8785V1)
	}
	return nil
}

func storedCanonicalHash(domain CanonicalHashDomain, digest []byte, canonicalizer string) (CanonicalJSONHash, error) {
	if !validCanonicalHashDomain(domain) {
		return CanonicalJSONHash{}, fmt.Errorf("unknown stored canonical hash domain %q", domain)
	}
	if len(digest) != sha256.Size {
		return CanonicalJSONHash{}, fmt.Errorf("stored %s hash has %d bytes", domain, len(digest))
	}
	if canonicalizer != CanonicalizerRFC8785V1 {
		return CanonicalJSONHash{}, fmt.Errorf("stored %s hash uses unsupported canonicalizer %q", domain, canonicalizer)
	}
	var value [sha256.Size]byte
	copy(value[:], digest)
	return CanonicalJSONHash{domain: domain, digest: value, canonicalizer: canonicalizer}, nil
}

type canonicalHashJSONFrame struct {
	object    bool
	expectKey bool
	keys      map[string]struct{}
}

func validateCanonicalHashJSONTokens(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	frames := make([]canonicalHashJSONFrame, 0, 8)
	tokens := 0
	rootValues := 0
	completeValue := func() error {
		if len(frames) == 0 {
			rootValues++
			if rootValues > 1 {
				return errors.New("canonical hash JSON contains more than one top-level value")
			}
			return nil
		}
		parent := &frames[len(frames)-1]
		if parent.object {
			if parent.expectKey {
				return errors.New("canonical hash JSON object is missing a key")
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
			return fmt.Errorf("decode canonical hash JSON token: %w", err)
		}
		tokens++
		if tokens > maxCanonicalHashJSONTokens {
			return fmt.Errorf("canonical hash JSON contains more than %d tokens", maxCanonicalHashJSONTokens)
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				frames = append(frames, canonicalHashJSONFrame{object: true, expectKey: true, keys: make(map[string]struct{})})
			case '[':
				frames = append(frames, canonicalHashJSONFrame{})
			case '}', ']':
				if len(frames) == 0 {
					return errors.New("canonical hash JSON has an unmatched closing delimiter")
				}
				frame := frames[len(frames)-1]
				if delimiter == '}' && (!frame.object || !frame.expectKey) {
					return errors.New("canonical hash JSON object ended while expecting a value")
				}
				if delimiter == ']' && frame.object {
					return errors.New("canonical hash JSON array ended with an object delimiter")
				}
				frames = frames[:len(frames)-1]
				if err := completeValue(); err != nil {
					return err
				}
			}
			if len(frames) > maxCanonicalHashJSONDepth {
				return fmt.Errorf("canonical hash JSON nesting exceeds %d", maxCanonicalHashJSONDepth)
			}
			continue
		}

		if len(frames) > 0 {
			frame := &frames[len(frames)-1]
			if frame.object && frame.expectKey {
				key, ok := token.(string)
				if !ok {
					return errors.New("canonical hash JSON object key is not a string")
				}
				if _, duplicate := frame.keys[key]; duplicate {
					return fmt.Errorf("duplicate canonical hash JSON object key %q", key)
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
		return errors.New("canonical hash JSON ended with an open container")
	}
	if rootValues != 1 {
		return errors.New("canonical hash JSON must contain exactly one top-level value")
	}
	return nil
}
