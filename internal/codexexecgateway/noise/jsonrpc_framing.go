package noise

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// NoiseJsonRpcLengthPrefixBytes is the 4-byte big-endian length prefix
// codex's noise relay virtual streams add to every JSON-RPC message.
// See codex-rs/exec-server/src/noise_relay/message_framing.rs — without
// this prefix codex's JsonRpcMessageDecoder reads the first 4 JSON
// bytes as a length and aborts the stream with
// "Noise relay JSON-RPC message has invalid length".
const NoiseJsonRpcLengthPrefixBytes = 4

// NoiseRecordPlaintextLen is the per-record plaintext byte limit codex
// uses to split large JSON-RPC messages into Noise records (60KB). One
// JSON-RPC message can span many records; one record can finish many
// short messages. Match this exactly so the gateway's outbound chunking
// matches what the executor's reassembly expects.
const NoiseRecordPlaintextLen = 60 * 1024

// MaxNoiseJsonRpcMessageLen is the upper bound the executor's decoder
// will accept for a single message (64 MiB). The gateway uses it as a
// reassembly cap on inbound traffic.
const MaxNoiseJsonRpcMessageLen = 64 * 1024 * 1024

// FrameOutboundMessage prefixes one JSON-RPC payload with the noise
// relay's authenticated big-endian u32 length and splits the result
// into NoiseRecordPlaintextLen-bounded records ready for one
// Transport.Encrypt call per record.
//
// The caller passes each returned slice through their NoiseTransport's
// Encrypt in order; each becomes one RelayData frame on the wire.
func FrameOutboundMessage(payload []byte) ([][]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("noise framer: empty JSON-RPC payload")
	}
	if len(payload) > MaxNoiseJsonRpcMessageLen {
		return nil, fmt.Errorf("noise framer: payload %d > %d max", len(payload), MaxNoiseJsonRpcMessageLen)
	}
	framed := make([]byte, NoiseJsonRpcLengthPrefixBytes+len(payload))
	binary.BigEndian.PutUint32(framed[:NoiseJsonRpcLengthPrefixBytes], uint32(len(payload)))
	copy(framed[NoiseJsonRpcLengthPrefixBytes:], payload)

	if len(framed) <= NoiseRecordPlaintextLen {
		return [][]byte{framed}, nil
	}
	records := make([][]byte, 0, (len(framed)+NoiseRecordPlaintextLen-1)/NoiseRecordPlaintextLen)
	for i := 0; i < len(framed); i += NoiseRecordPlaintextLen {
		end := i + NoiseRecordPlaintextLen
		if end > len(framed) {
			end = len(framed)
		}
		records = append(records, framed[i:end])
	}
	return records, nil
}

// InboundReassembler buffers decrypted plaintext records as they arrive
// from the peer and emits complete JSON-RPC messages (the
// authenticated length prefix stripped). One inbound record can
// complete zero, one, or many messages; one message can span many
// records.
//
// This mirrors codex's JsonRpcMessageDecoder; both peers MUST agree on
// the exact reassembly rules or the wire goes one frame and never
// recovers.
type InboundReassembler struct {
	buf []byte
}

// Push appends one decrypted record (the plaintext from one
// Transport.Decrypt call) and returns any complete JSON-RPC messages
// it finishes. Each returned slice is a fresh allocation safe to
// hand off to caller goroutines.
func (r *InboundReassembler) Push(record []byte) ([][]byte, error) {
	if len(record) > NoiseRecordPlaintextLen {
		return nil, fmt.Errorf("noise reassembler: record %d > %d max", len(record), NoiseRecordPlaintextLen)
	}
	r.buf = append(r.buf, record...)

	var messages [][]byte
	for {
		if len(r.buf) < NoiseJsonRpcLengthPrefixBytes {
			break
		}
		msgLen := int(binary.BigEndian.Uint32(r.buf[:NoiseJsonRpcLengthPrefixBytes]))
		// Authenticated length already; reject before waiting for payload
		// bytes that may never arrive.
		if msgLen == 0 || msgLen > MaxNoiseJsonRpcMessageLen {
			return nil, fmt.Errorf("noise reassembler: invalid declared length %d", msgLen)
		}
		framedLen := NoiseJsonRpcLengthPrefixBytes + msgLen
		if len(r.buf) < framedLen {
			break
		}
		// Copy out so we can shrink the internal buffer without aliasing.
		msg := make([]byte, msgLen)
		copy(msg, r.buf[NoiseJsonRpcLengthPrefixBytes:framedLen])
		messages = append(messages, msg)
		r.buf = r.buf[framedLen:]
	}

	// Bound the partial buffer even while no message has completed —
	// matches the executor-side guard so a hostile peer can't grow it
	// unbounded by sending records that never close a message.
	if len(r.buf) > NoiseJsonRpcLengthPrefixBytes+MaxNoiseJsonRpcMessageLen {
		return nil, fmt.Errorf("noise reassembler: partial buffer exceeds %d bytes",
			NoiseJsonRpcLengthPrefixBytes+MaxNoiseJsonRpcMessageLen)
	}
	return messages, nil
}
