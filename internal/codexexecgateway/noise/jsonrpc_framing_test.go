package noise

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestFrameOutboundMessage_SingleRecord(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	records, err := FrameOutboundMessage(payload)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if len(records[0]) != 4+len(payload) {
		t.Errorf("record len = %d, want %d", len(records[0]), 4+len(payload))
	}
	// Round-trip: a reassembler fed the (single) record produces the
	// original payload.
	var rs InboundReassembler
	msgs, err := rs.Push(records[0])
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(msgs) != 1 || !bytes.Equal(msgs[0], payload) {
		t.Errorf("round-trip mismatch")
	}
}

func TestFrameOutboundMessage_SplitsAcrossRecords(t *testing.T) {
	payload := make([]byte, 200*1024) // 200 KB → 4× 60 KB records
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	records, err := FrameOutboundMessage(payload)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if len(records) < 3 {
		t.Errorf("expected multi-record split, got %d", len(records))
	}
	var rs InboundReassembler
	var got [][]byte
	for _, rec := range records {
		out, err := rs.Push(rec)
		if err != nil {
			t.Fatalf("push: %v", err)
		}
		got = append(got, out...)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 reassembled message, got %d", len(got))
	}
	if !bytes.Equal(got[0], payload) {
		t.Errorf("payload mismatch after split/reassemble")
	}
}

func TestInboundReassembler_HandlesManyMessagesPerRecord(t *testing.T) {
	// Three small messages packed back-to-back in one record stream.
	var combined []byte
	want := [][]byte{[]byte(`{"id":1}`), []byte(`{"id":22}`), []byte(`{"id":333}`)}
	for _, m := range want {
		records, _ := FrameOutboundMessage(m)
		for _, r := range records {
			combined = append(combined, r...)
		}
	}
	var rs InboundReassembler
	got, err := rs.Push(combined)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("msg %d mismatch", i)
		}
	}
}

func TestInboundReassembler_HandlesByteAtATime(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":99,"result":{"sessionId":"abc"}}`)
	framed, _ := FrameOutboundMessage(payload)
	var rs InboundReassembler
	var got [][]byte
	for _, b := range framed[0] {
		out, err := rs.Push([]byte{b})
		if err != nil {
			t.Fatalf("push: %v", err)
		}
		got = append(got, out...)
	}
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Errorf("byte-at-a-time reassembly failed")
	}
}

func TestInboundReassembler_RejectsBogusLength(t *testing.T) {
	// First four bytes form a 0 length — rejected outright.
	var rs InboundReassembler
	if _, err := rs.Push([]byte{0, 0, 0, 0, 0xff}); err == nil {
		t.Errorf("expected error on zero-length prefix")
	}
}

func TestFrameOutboundMessage_RejectsEmpty(t *testing.T) {
	if _, err := FrameOutboundMessage(nil); err == nil {
		t.Errorf("expected error on empty payload")
	}
}
