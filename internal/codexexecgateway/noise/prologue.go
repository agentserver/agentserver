package noise

import "encoding/binary"

// Prologue returns the Noise prologue bytes for one bridge stream,
// matching codex's noise_channel_prologue exactly (length-prefixed
// concatenation, big-endian u64 lengths).
func Prologue(environmentID, executorRegistrationID, streamID string) []byte {
	parts := [][]byte{
		[]byte(PrologueDomain),
		[]byte(environmentID),
		[]byte(executorRegistrationID),
		[]byte(streamID),
	}
	total := 0
	for _, p := range parts {
		total += 8 + len(p)
	}
	out := make([]byte, 0, total)
	var lenBuf [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		out = append(out, lenBuf[:]...)
		out = append(out, p...)
	}
	return out
}
