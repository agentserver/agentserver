// Package larkegresspolicy is the single compiled definition of the first
// managed Lark pack. Its canonical document is hashed into every grant and
// frozen run so a deployment cannot silently reinterpret an older authority.
package larkegresspolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

const (
	PackID      = "lark-readonly@v1"
	OpenAPIHost = "open.feishu.cn"

	// CanonicalDocument is deliberately literal and whitespace-free. Changing
	// any rule requires a new pack version; mutating this document in place
	// would invalidate already-frozen grants and runs fail closed.
	CanonicalDocument = `{"version":1,"packId":"lark-readonly@v1","host":"open.feishu.cn","oauthScopes":["docx:document:readonly","offline_access","wiki:node:read"],"rules":[{"method":"GET","path":"/open-apis/wiki/v2/spaces/get_node"},{"method":"GET","pathTemplate":"/open-apis/docx/v1/documents/{document_id}"},{"method":"GET","pathTemplate":"/open-apis/docx/v1/documents/{document_id}/raw_content"},{"method":"GET","pathTemplate":"/open-apis/docx/v1/documents/{document_id}/blocks"},{"method":"GET","pathTemplate":"/open-apis/docx/v1/documents/{document_id}/blocks/{block_id}"},{"method":"GET","pathTemplate":"/open-apis/docx/v1/documents/{document_id}/blocks/{block_id}/children"}]}`
)

var policySHA256 = sha256.Sum256([]byte(CanonicalDocument))

func SHA256() [sha256.Size]byte {
	return policySHA256
}

func SHA256Hex() string {
	return hex.EncodeToString(policySHA256[:])
}

func Allows(host, requestPath, method string) bool {
	if host != OpenAPIHost || method != "GET" || requestPath == "" ||
		!strings.HasPrefix(requestPath, "/") || path.Clean(requestPath) != requestPath {
		return false
	}
	if requestPath == "/open-apis/wiki/v2/spaces/get_node" {
		return true
	}
	segments := strings.Split(strings.TrimPrefix(requestPath, "/"), "/")
	if len(segments) < 5 || segments[0] != "open-apis" || segments[1] != "docx" || segments[2] != "v1" ||
		segments[3] != "documents" || !validPathID(segments[4]) {
		return false
	}
	switch len(segments) {
	case 5:
		return true
	case 6:
		return segments[5] == "raw_content" || segments[5] == "blocks"
	case 7:
		return segments[5] == "blocks" && validPathID(segments[6])
	case 8:
		return segments[5] == "blocks" && validPathID(segments[6]) && segments[7] == "children"
	default:
		return false
	}
}

func validPathID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}
