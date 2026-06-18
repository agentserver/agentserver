package noise

import (
	"bytes"
	"testing"
)

// Mirrors codex's hybrid_ik_roundtrip_authenticates_both_endpoints
// (codex-rs/exec-server/src/noise_channel_tests.rs:12-64).
func TestHybridIK_Roundtrip(t *testing.T) {
	initiator := mustIdentity(t)
	responder := mustIdentity(t)
	prologue := Prologue("env-1", "registration-1", "stream-1")
	authorization := []byte("harness-key-authorization")

	hs, msg1, err := StartInitiator(initiator, responder.PublicKey(), prologue, authorization)
	if err != nil {
		t.Fatalf("start initiator: %v", err)
	}

	resp, err := ReadInitiatorRequest(responder, prologue, msg1)
	if err != nil {
		t.Fatalf("read initiator request: %v", err)
	}
	if !bytes.Equal(resp.InitiatorPayload, authorization) {
		t.Fatalf("payload mismatch\n got: %x\nwant: %x", resp.InitiatorPayload, authorization)
	}
	if resp.InitiatorPubKey != initiator.PublicKey() {
		t.Fatalf("learned initiator pubkey mismatch")
	}

	respTransport, msg2, err := resp.Complete()
	if err != nil {
		t.Fatalf("responder complete: %v", err)
	}
	initTransport, err := hs.Finish(msg2)
	if err != nil {
		t.Fatalf("initiator finish: %v", err)
	}

	// Request: initiator → responder
	requestCT, err := initTransport.Encrypt([]byte("request"))
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}
	if bytes.Equal(requestCT, []byte("request")) {
		t.Fatalf("encrypt returned plaintext")
	}
	pt, err := respTransport.Decrypt(requestCT)
	if err != nil {
		t.Fatalf("decrypt request: %v", err)
	}
	if !bytes.Equal(pt, []byte("request")) {
		t.Fatalf("decrypt mismatch")
	}

	// Response: responder → initiator
	responseCT, err := respTransport.Encrypt([]byte("response"))
	if err != nil {
		t.Fatalf("encrypt response: %v", err)
	}
	pt, err = initTransport.Decrypt(responseCT)
	if err != nil {
		t.Fatalf("decrypt response: %v", err)
	}
	if !bytes.Equal(pt, []byte("response")) {
		t.Fatalf("response decrypt mismatch")
	}
}

func TestHybridIK_Msg1WireSize(t *testing.T) {
	initiator := mustIdentity(t)
	responder := mustIdentity(t)
	prologue := Prologue("e", "r", "s")
	payload := make([]byte, 100)
	_, msg1, err := StartInitiator(initiator, responder.PublicKey(), prologue, payload)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Skem ct (1088, no tag) + E.dh (32) + E.kem (1184) + S.dh enc (48) + S.kem enc (1200) + payload enc (P+16)
	want := MLKEM768CiphertextLen + X25519PubLen + MLKEM768PubLen + (X25519PubLen + AESGCMTagLen) + (MLKEM768PubLen + AESGCMTagLen) + len(payload) + AESGCMTagLen
	if len(msg1) != want {
		t.Errorf("msg1 wire len = %d, want %d", len(msg1), want)
	}
}

func TestHybridIK_Msg2WireSize(t *testing.T) {
	initiator := mustIdentity(t)
	responder := mustIdentity(t)
	prologue := Prologue("e", "r", "s")
	_, msg1, err := StartInitiator(initiator, responder.PublicKey(), prologue, []byte("x"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	resp, err := ReadInitiatorRequest(responder, prologue, msg1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_, msg2, err := resp.Complete()
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Ekem ct (1088, no tag) + Skem ct enc (1088+16) + E.dh (32) + E.kem (1184) + empty payload tag (16)
	want := MLKEM768CiphertextLen + (MLKEM768CiphertextLen + AESGCMTagLen) + X25519PubLen + MLKEM768PubLen + AESGCMTagLen
	if len(msg2) != want {
		t.Errorf("msg2 wire len = %d, want %d", len(msg2), want)
	}
}

func TestHybridIK_RejectsWrongResponderKey(t *testing.T) {
	initiator := mustIdentity(t)
	expectedResponder := mustIdentity(t)
	actualResponder := mustIdentity(t)
	prologue := Prologue("env-1", "registration-1", "stream-1")
	_, msg1, err := StartInitiator(initiator, expectedResponder.PublicKey(), prologue, []byte("a"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := ReadInitiatorRequest(actualResponder, prologue, msg1); err == nil {
		t.Fatalf("wrong-responder read should have failed")
	}
}

func TestHybridIK_RejectsMismatchedPrologue(t *testing.T) {
	initiator := mustIdentity(t)
	responder := mustIdentity(t)
	pInit := Prologue("env-1", "registration-1", "stream-1")
	pResp := Prologue("env-1", "registration-1", "stream-2")
	_, msg1, err := StartInitiator(initiator, responder.PublicKey(), pInit, []byte("a"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := ReadInitiatorRequest(responder, pResp, msg1); err == nil {
		t.Fatalf("mismatched prologue should have failed")
	}
}

func TestTransport_RejectsTamperedCiphertext(t *testing.T) {
	initT, respT := paired(t)
	ct, err := initT.Encrypt([]byte("request"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct[0] ^= 1
	if _, err := respT.Decrypt(ct); err == nil {
		t.Fatalf("tampered decrypt should have failed")
	}
}

func TestTransport_RejectsReplayedCiphertext(t *testing.T) {
	initT, respT := paired(t)
	ct, err := initT.Encrypt([]byte("request"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := respT.Decrypt(ct); err != nil {
		t.Fatalf("first decrypt: %v", err)
	}
	if _, err := respT.Decrypt(ct); err == nil {
		t.Fatalf("replay decrypt should have failed")
	}
}

func paired(t testing.TB) (*Transport, *Transport) {
	t.Helper()
	initiator := mustIdentity(t)
	responder := mustIdentity(t)
	prologue := Prologue("env-1", "registration-1", "stream-1")
	hs, msg1, err := StartInitiator(initiator, responder.PublicKey(), prologue, []byte("auth"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	resp, err := ReadInitiatorRequest(responder, prologue, msg1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	respT, msg2, err := resp.Complete()
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	initT, err := hs.Finish(msg2)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return initT, respT
}

func mustIdentity(t testing.TB) *Identity {
	t.Helper()
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	return id
}
