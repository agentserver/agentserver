// Package noise implements the Noise channel used by codex-exec-gateway
// to terminate codex's exec-server noise relay protocol on the
// initiator (harness) side.
//
// Suite: Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256
//
// The pattern is hybrid IK ("hybrid" = both classical X25519 and post-
// quantum ML-KEM-768 contribute to the session keys):
//
//	<- s, skem                              (pre-message: responder static pinned)
//	-> skem, e, es, s, ss                   (msg 1, initiator outbound)
//	<- ekem, skem, e, ee, se                (msg 2, responder outbound)
//
// Token semantics per codex's clatter usage (handshakestate/hybrid.rs):
//
//	E       generate (DH ephemeral, KEM ephemeral); send DH pub + KEM pub;
//	        MixHash both. ML-KEM-768 ephemeral is the same algorithm as the
//	        static KEM, just a different keypair role.
//	S       encrypt-and-hash DH static pub then KEM static pub.
//	ES,SE,
//	EE,SS   classical X25519 DH using the relevant keypair pair; MixKey.
//	Ekem    sender encapsulates fresh shared secret to peer's ephemeral KEM
//	        pubkey; send raw ciphertext; MixHash(ct) then MixKey(ss).
//	Skem    sender encapsulates fresh shared secret to peer's static KEM
//	        pubkey; encrypt-and-hash(ct); MixKeyAndHash(ss).
//
// Wire sizes (ML-KEM-768: 1184-byte pub, 1088-byte ct; AES-GCM 16-byte tag):
//
//	msg 1 (initiator → responder):
//	  Skem ct                 1088 bytes (no tag — symmetric key not active yet)
//	  E.dh pub                  32
//	  E.kem pub               1184
//	  S.dh pub enc              48 (32 + 16 tag)
//	  S.kem pub enc           1200 (1184 + 16 tag)
//	  payload enc                P + 16 tag
//	  ────────────────────────────────────
//	  total                   3552 + P bytes
//
//	msg 2 (responder → initiator):
//	  Ekem ct                 1088 bytes (no tag — Ekem is mix_hash/mix_key, not encrypt_and_hash)
//	  Skem ct enc             1104 (1088 + 16 tag)
//	  E.dh pub                  32
//	  E.kem pub               1184
//	  payload enc (empty)       16 (just the tag)
//	  ────────────────────────────────────
//	  total                   3424 bytes
//
// Prologue binds the handshake to a specific (environment_id,
// executor_registration_id, stream_id) triple, length-prefixed:
//
//	prologue = u64be(len(D)) || D || u64be(len(env)) || env
//	         || u64be(len(reg)) || reg || u64be(len(stream)) || stream
//	D = "codex-exec-server-relay-noise/v1"
//
// Transport phase: AES-256-GCM with per-direction implicit 64-bit
// little-endian nonce starting at 0. Each call increments. Max frame
// size 65535 (Noise spec MAX_MESSAGE_LEN).
//
// Bit-compat against codex's clatter is gating per spec
// docs/superpowers/specs/2026-06-18-codex-exec-gateway-noise-relay-design.md
// §7 Phase 1. If Go stdlib mlkem / ecdh / hkdf produce divergent bytes
// at any handshake step, fall back to the Rust FFI plan in the spec.
package noise
