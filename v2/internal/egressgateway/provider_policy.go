package egressgateway

import (
	"errors"
	"net"
	"path"
	"strings"
)

type ProviderPolicyRule struct {
	ProviderKind string
	Host         string
	PolicySHA256 string
	Methods      []string
	PathPrefixes []string
}

type StaticProviderPolicy struct {
	rules []ProviderPolicyRule
}

func NewStaticProviderPolicy(rules []ProviderPolicyRule) (*StaticProviderPolicy, error) {
	if len(rules) == 0 || len(rules) > 256 {
		return nil, errors.New("provider egress policy must contain between one and 256 rules")
	}
	copyRules := make([]ProviderPolicyRule, len(rules))
	for index, rule := range rules {
		if !canonicalPolicyToken(rule.ProviderKind, 128) || !canonicalPolicyHost(rule.Host) ||
			!lowerHexDigest(rule.PolicySHA256) || len(rule.Methods) == 0 || len(rule.Methods) > 16 || len(rule.PathPrefixes) == 0 || len(rule.PathPrefixes) > 64 {
			return nil, errors.New("provider egress policy rule is invalid")
		}
		for _, method := range rule.Methods {
			if method == "" || method != strings.ToUpper(method) || strings.ContainsAny(method, " \t\r\n") {
				return nil, errors.New("provider egress policy method is invalid")
			}
		}
		for _, prefix := range rule.PathPrefixes {
			if prefix == "" || len(prefix) > 4096 || !strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "\\%?#\x00") ||
				path.Clean(prefix) != prefix || (len(prefix) > 1 && strings.HasSuffix(prefix, "/")) {
				return nil, errors.New("provider egress policy path prefix is invalid")
			}
		}
		copyRules[index] = rule
		copyRules[index].Methods = append([]string(nil), rule.Methods...)
		copyRules[index].PathPrefixes = append([]string(nil), rule.PathPrefixes...)
	}
	return &StaticProviderPolicy{rules: copyRules}, nil
}

func canonicalPolicyToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_' || character == '.' || character == ':')) {
			continue
		}
		return false
	}
	return true
}

func canonicalPolicyHost(host string) bool {
	if host == "" || len(host) > 512 || host != strings.ToLower(host) || net.ParseIP(host) != nil ||
		strings.ContainsAny(host, "/:@[]") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !asciiAlphaNumeric(label[0]) || !asciiAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
}

func lowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func (policy *StaticProviderPolicy) Allows(providerKind, host, requestPath, method, policySHA256 string) bool {
	if policy == nil {
		return false
	}
	for _, rule := range policy.rules {
		if rule.ProviderKind != providerKind || rule.Host != host || rule.PolicySHA256 != policySHA256 {
			continue
		}
		methodAllowed := false
		for _, allowed := range rule.Methods {
			if allowed == method {
				methodAllowed = true
				break
			}
		}
		if !methodAllowed {
			continue
		}
		for _, prefix := range rule.PathPrefixes {
			if requestPath == prefix || prefix == "/" || strings.HasPrefix(requestPath, prefix+"/") {
				return true
			}
		}
	}
	return false
}

var _ ProviderEgressPolicy = (*StaticProviderPolicy)(nil)
