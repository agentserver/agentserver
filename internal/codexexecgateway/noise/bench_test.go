package noise

import "testing"

func BenchmarkHandshakeRoundtrip(b *testing.B) {
	initiator, err := GenerateIdentity()
	if err != nil {
		b.Fatal(err)
	}
	responder, err := GenerateIdentity()
	if err != nil {
		b.Fatal(err)
	}
	prologue := Prologue("env-1", "registration-1", "stream-1")
	payload := []byte("harness-key-authorization")
	respPub := responder.PublicKey()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hs, msg1, err := StartInitiator(initiator, respPub, prologue, payload)
		if err != nil {
			b.Fatal(err)
		}
		rh, err := ReadInitiatorRequest(responder, prologue, msg1)
		if err != nil {
			b.Fatal(err)
		}
		_, msg2, err := rh.Complete()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := hs.Finish(msg2); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransportEncrypt1KB(b *testing.B) {
	initT, _ := paired(b)
	plaintext := make([]byte, 1024)
	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := initT.Encrypt(plaintext); err != nil {
			b.Fatal(err)
		}
	}
}
