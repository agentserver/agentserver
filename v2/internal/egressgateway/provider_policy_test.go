package egressgateway

import (
	"strings"
	"testing"
)

func TestStaticProviderPolicyRequiresAPathSegmentBoundary(t *testing.T) {
	digest := strings.Repeat("a", 64)
	policy, err := NewStaticProviderPolicy([]ProviderPolicyRule{{
		ProviderKind: "lark", Host: "open.feishu.cn", PolicySHA256: digest,
		Methods: []string{"GET"}, PathPrefixes: []string{"/open-apis/docx/v1/documents"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []string{
		"/open-apis/docx/v1/documents",
		"/open-apis/docx/v1/documents/abc",
	} {
		if !policy.Allows("lark", "open.feishu.cn", allowed, "GET", digest) {
			t.Fatalf("expected path %q to be allowed", allowed)
		}
	}
	for _, denied := range []string{
		"/open-apis/docx/v1/documentsevil",
		"/open-apis/docx/v1/documents-old/abc",
	} {
		if policy.Allows("lark", "open.feishu.cn", denied, "GET", digest) {
			t.Fatalf("adjacent path %q must not match the prefix", denied)
		}
	}
}

func TestStaticProviderPolicyRejectsNonCanonicalPrefixes(t *testing.T) {
	digest := strings.Repeat("b", 64)
	for _, prefix := range []string{"/a/", "/a/../b", "/a%2fb", "//a"} {
		if _, err := NewStaticProviderPolicy([]ProviderPolicyRule{{
			ProviderKind: "lark", Host: "open.feishu.cn", PolicySHA256: digest,
			Methods: []string{"GET"}, PathPrefixes: []string{prefix},
		}}); err == nil {
			t.Fatalf("expected prefix %q to be rejected", prefix)
		}
	}
}
