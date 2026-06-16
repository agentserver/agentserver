// Package secrets generates and validates prefix-id-secret style
// credentials used across agentserver. Centralizes the
// `rand.Read + encode + sha256 + hex` pattern that was previously
// duplicated across workspace_api_keys, codex_token_format,
// proxy_tokens, and a few smaller call sites.
//
// Format on the wire: <prefix>_<id>_<secret><crc32>
//   - prefix is a short ASCII tag (e.g. "ask", "ast") with a trailing _
//     in the Spec (so the full wire token reads "ask_...").
//   - id is a public, indexable handle (DB primary key); base62 encoded
//   - secret is the high-entropy payload; base62 encoded
//   - crc32 is a 6-char base62-encoded CRC32/IEEE checksum of the preceding
//     <prefix>_<id>_<secret> portion (no separator). Same scheme as GitHub.
//   - hash = HMAC-SHA256(pepper, <full token>) or SHA-256 if no pepper —
//     the value persisted in the database.
//
// Encoding is base62 (0-9, a-z, A-Z) for ~5.95 bits/character.
// CRC32/IEEE (32-bit) encodes to ceil(32/log2(62)) = 6 chars with leading-
// zero padding. SHA-256 is used for hashing because secrets are CSPRNG-
// generated (>= ~140 bits of entropy at SecretLen=24+); bcrypt's KDF
// overhead adds nothing against random secrets and just slows validation.
package secrets

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math/big"
	"strings"
	"sync"
)

// base62Alphabet is the canonical ordering for base62-encoded credentials.
// Order: digits first (0-9), then lowercase (a-z), then uppercase (A-Z).
// Matches the de-facto convention used by Stripe / OpenAI tokens.
const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// crc32Len is the fixed number of base62 chars used to encode a CRC32
// checksum. 4 bytes (32 bits) in base62: ceil(32 / log2(62)) = 6 chars.
const crc32Len = 6

// Spec defines the shape of a credential type. One Spec per kind.
type Spec struct {
	// Prefix is the human-readable type tag. MUST end in "_".
	// Examples: "ask_" (workspace API secret key), "ast_" (agentserver
	// remote token used as AGENTSERVER_TOKEN by the codex CLI).
	Prefix string
	// IDLen is the number of base62 chars in the public id segment.
	// 8 is the project default; gives 47 bits of entropy in the id alone,
	// enough to make collisions vanishingly unlikely across the lifetime
	// of any one workspace.
	IDLen int
	// SecretLen is the number of base62 chars in the secret segment.
	// 40 chars = ~238 bits, well above any cryptographic threshold.
	SecretLen int
}

// bodyLen returns the length of the token body (everything before the CRC32
// suffix): len(Prefix) + IDLen + 1 (separator "_") + SecretLen.
func (s Spec) bodyLen() int {
	return len(s.Prefix) + s.IDLen + 1 + s.SecretLen
}

// Token is a freshly minted credential. Only `Full` ever leaves the
// server (returned to the user once, never stored). `Hash` is what gets
// persisted; presented secrets are compared via ConstantTimeMatch.
type Token struct {
	// Full is what the user receives: "<prefix>_<id>_<secret><crc32>".
	// Caller MUST present this back as `Authorization: Bearer <Full>`.
	Full string
	// ID is "<prefix>_<id>" — the public, indexable handle.
	// Use as DB primary key for the row.
	ID string
	// Secret is the bare secret segment (no prefix, no id, no crc32).
	// Most callers don't need to store this directly — store Hash instead.
	Secret string
	// Hash is the storage hash of Full (HMAC-SHA256 with pepper if set,
	// otherwise plain SHA-256). Persist this; never persist Full or Secret.
	Hash string
}

// pepper is the server-side HMAC key used by Hash. Set once at process
// startup via SetPepper. When unset (typical in dev), Hash falls back
// to plain SHA-256 so unit tests + local development don't require the
// env var. Production deployments MUST set this; the value is rotated
// once-and-never (rotating invalidates all existing hashes).
var (
	pepperMu sync.RWMutex
	pepper   []byte
)

// SetPepper installs the server-side HMAC key. Call once at startup;
// safe to call concurrently with Hash() but the typical flow is
// startup-only. Idempotent — same value reset is a no-op; a different
// value panics (we'd rather crash than silently break all stored hashes).
func SetPepper(b []byte) {
	pepperMu.Lock()
	defer pepperMu.Unlock()
	if pepper != nil && subtle.ConstantTimeCompare(pepper, b) != 1 {
		panic("secrets: SetPepper called twice with different values — rotating the pepper invalidates all stored hashes")
	}
	pepper = append([]byte(nil), b...)
}

// Mint generates a new credential matching spec. Reads from crypto/rand
// and rejects bad specs (empty/no-underscore prefix, zero lengths) so
// misconfigured callers fail loud at startup rather than at the first mint.
func Mint(spec Spec) (Token, error) {
	if err := validateSpec(spec); err != nil {
		return Token{}, err
	}
	idPart, err := randomBase62(spec.IDLen)
	if err != nil {
		return Token{}, fmt.Errorf("secrets: gen id: %w", err)
	}
	secret, err := randomBase62(spec.SecretLen)
	if err != nil {
		return Token{}, fmt.Errorf("secrets: gen secret: %w", err)
	}
	id := spec.Prefix + idPart
	body := id + "_" + secret
	crc := crc32Base62(body)
	full := body + crc
	return Token{
		Full:   full,
		ID:     id,
		Secret: secret,
		Hash:   Hash(full),
	}, nil
}

// Parse validates wire shape and splits a presented full token into
// (id, secret) without doing any crypto beyond the CRC32 integrity check.
// Returns ErrInvalidFormat on any structural mismatch (wrong prefix, wrong
// segment lengths, missing underscore separator, non-base62 character, or
// CRC32 mismatch).
//
// Used by middleware to extract the indexable id before the DB lookup,
// so a malformed token never hits the DB.
func Parse(spec Spec, full string) (id, secret string, err error) {
	if err := validateSpec(spec); err != nil {
		return "", "", err
	}
	bodyLen := spec.bodyLen()
	wantLen := bodyLen + crc32Len
	if len(full) != wantLen {
		return "", "", ErrInvalidFormat
	}
	body := full[:bodyLen]
	presentedCRC := full[bodyLen:]

	// Validate CRC32 with constant-time compare to avoid timing leak on CRC.
	expectedCRC := crc32Base62(body)
	if subtle.ConstantTimeCompare([]byte(presentedCRC), []byte(expectedCRC)) != 1 {
		return "", "", ErrInvalidFormat
	}

	// Parse the body: <prefix><idPart>_<secretPart>
	if !strings.HasPrefix(body, spec.Prefix) {
		return "", "", ErrInvalidFormat
	}
	rest := body[len(spec.Prefix):]
	sep := strings.IndexByte(rest, '_')
	if sep != spec.IDLen {
		return "", "", ErrInvalidFormat
	}
	if len(rest)-sep-1 != spec.SecretLen {
		return "", "", ErrInvalidFormat
	}
	idPart := rest[:sep]
	secretPart := rest[sep+1:]
	if !isBase62(idPart) || !isBase62(secretPart) {
		return "", "", ErrInvalidFormat
	}
	return spec.Prefix + idPart, secretPart, nil
}

// Hash returns the storage hash for full. When a pepper is configured
// (production), it's HMAC-SHA256(pepper, full); otherwise plain SHA-256.
// Both produce 64-char lowercase hex.
func Hash(full string) string {
	pepperMu.RLock()
	p := pepper
	pepperMu.RUnlock()
	if p == nil {
		sum := sha256.Sum256([]byte(full))
		return hex.EncodeToString(sum[:])
	}
	mac := hmac.New(sha256.New, p)
	mac.Write([]byte(full))
	return hex.EncodeToString(mac.Sum(nil))
}

// ConstantTimeMatch hashes presented and constant-time compares to
// storedHash. Returns false on any mismatch including length difference.
// Use this in auth paths to avoid timing leaks on prefix collisions.
func ConstantTimeMatch(presented, storedHash string) bool {
	h := Hash(presented)
	return subtle.ConstantTimeCompare([]byte(h), []byte(storedHash)) == 1
}

// RandomHex returns 2*nBytes hex chars from crypto/rand. For sites that
// just need an opaque bare-hex token (session cookies, internal proxy
// tokens) and don't want the prefix-id-secret structure. Equivalent to
// `rand.Read(b); hex.EncodeToString(b)` but centralizes the pattern.
func RandomHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", fmt.Errorf("secrets: RandomHex needs nBytes > 0")
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secrets: rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ErrInvalidFormat is returned by Parse when the presented token doesn't
// match the Spec's wire shape.
var ErrInvalidFormat = fmt.Errorf("secrets: invalid token format")

// validateSpec ensures a Spec is internally consistent. Called from Mint
// and Parse — package-level Specs that get the values wrong fail at
// program startup rather than at first mint.
func validateSpec(spec Spec) error {
	if !strings.HasSuffix(spec.Prefix, "_") {
		return fmt.Errorf("secrets: Spec.Prefix %q must end with _", spec.Prefix)
	}
	if spec.IDLen <= 0 || spec.SecretLen <= 0 {
		return fmt.Errorf("secrets: Spec needs IDLen > 0 and SecretLen > 0")
	}
	return nil
}

// randomBase62 returns n base62 characters drawn from crypto/rand.
// Uses rand.Int with a power-of-62 ceiling to avoid modulo bias.
func randomBase62(n int) (string, error) {
	max := big.NewInt(int64(len(base62Alphabet)))
	out := make([]byte, n)
	for i := range out {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = base62Alphabet[k.Int64()]
	}
	return string(out), nil
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return true
}

// crc32Base62 computes CRC32/IEEE of s and returns it as exactly crc32Len
// (6) base62 characters, left-padded with '0' if the value is small.
// No separator between the token body and the CRC suffix — same as GitHub.
func crc32Base62(s string) string {
	sum := crc32.ChecksumIEEE([]byte(s))
	// Encode the 32-bit value as base62 with fixed width crc32Len.
	out := make([]byte, crc32Len)
	v := uint64(sum)
	base := uint64(len(base62Alphabet))
	for i := crc32Len - 1; i >= 0; i-- {
		out[i] = base62Alphabet[v%base]
		v /= base
	}
	return string(out)
}

// Pre-defined specs used by callers. Adding a new credential kind?
// Define its Spec here so the catalog of token formats is greppable.

// APIKeySpec is the format for per-workspace developer API keys
// ("Agentserver Secret Key" — `ask_` prefix). Minted via POST
// /api/workspaces/{wid}/api-keys; returned to the user as Token.Full once.
//
// AgentserverTokenSpec is the format for the workspace-scoped codex remote
// token ("Agentserver Token" — `ast_` prefix) that users export as
// AGENTSERVER_TOKEN and pass to `codex --remote --remote-auth-token-env`.
// Same algorithm, different prefix so leaked-token scanners can distinguish.
//
// MCPPATSpec is the format for user-scoped Personal Access Tokens used
// against the envmcp public gateway (`agpat_` prefix — "Agentserver
// Personal Access Token"). Spec'd in
// docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md § 4.3
// as the fallback auth for CI/automation; OAuth is the preferred path.
//
// Sizing rationale (shared by all three):
//   - IDLen=16 chars of base62 = ~95 bits. Globally collision-free for any
//     plausible total key count (birthday bound ~ 2^47 keys for 50% odds).
//   - SecretLen=48 chars of base62 = ~286 bits. Well past any cryptographic
//     threshold; oversized vs strictly necessary to leave headroom.
//   - Total wire length: 4 + 16 + 1 + 48 + 6 = 75 chars including CRC32
//     (76 for the 6-char `agpat_` prefix).
var (
	APIKeySpec           = Spec{Prefix: "ask_", IDLen: 16, SecretLen: 48}
	AgentserverTokenSpec = Spec{Prefix: "ast_", IDLen: 16, SecretLen: 48}
	MCPPATSpec           = Spec{Prefix: "agpat_", IDLen: 16, SecretLen: 48}
)
