package noise

import (
	"bytes"
	"testing"
)

// Mirrors codex's prologue_encoding_is_stable_and_unambiguous test
// (codex-rs/exec-server/src/noise_channel_tests.rs:105-117).
func TestPrologueMatchesCodex(t *testing.T) {
	got := Prologue("env-1", "registration-1", "stream-1")
	want := []byte("\x00\x00\x00\x00\x00\x00\x00\x20codex-exec-server-relay-noise/v1" +
		"\x00\x00\x00\x00\x00\x00\x00\x05env-1" +
		"\x00\x00\x00\x00\x00\x00\x00\x0eregistration-1" +
		"\x00\x00\x00\x00\x00\x00\x00\x08stream-1")
	if !bytes.Equal(got, want) {
		t.Fatalf("prologue mismatch\n got: %x\nwant: %x", got, want)
	}
}
