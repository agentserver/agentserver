package larkegresspolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCanonicalPolicyDigestAndRulesStayCoupled(t *testing.T) {
	want := sha256.Sum256([]byte(CanonicalDocument))
	if SHA256() != want || SHA256Hex() != hex.EncodeToString(want[:]) || len(SHA256Hex()) != 64 {
		t.Fatalf("managed Lark policy digest = %x / %q", SHA256(), SHA256Hex())
	}
	allowed := []string{
		"/open-apis/wiki/v2/spaces/get_node",
		"/open-apis/docx/v1/documents/doc_1",
		"/open-apis/docx/v1/documents/doc_1/raw_content",
		"/open-apis/docx/v1/documents/doc_1/blocks/block_1/children",
	}
	for _, requestPath := range allowed {
		if !Allows(OpenAPIHost, requestPath, "GET") {
			t.Fatalf("compiled policy denied %q", requestPath)
		}
	}
	for _, requestPath := range []string{
		"/open-apis/contact/v3/users",
		"/open-apis/docx/v1/documents/doc_1/blocks/block_1/children/extra",
	} {
		if Allows(OpenAPIHost, requestPath, "GET") {
			t.Fatalf("compiled policy allowed %q", requestPath)
		}
	}
	if Allows(OpenAPIHost, allowed[1], "POST") || Allows("open.larksuite.com", allowed[1], "GET") {
		t.Fatal("compiled policy allowed a different method or host")
	}
}
