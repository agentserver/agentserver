package codexwire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecoderHandlesBlankLinesCRLFAndFinalFrame(t *testing.T) {
	decoder, err := NewDecoder(strings.NewReader("\n \r\n{\"method\":\"initialized\"}\r\n{\"id\":1,\"result\":{}}"), 1024)
	if err != nil {
		t.Fatal(err)
	}

	first, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != KindNotification {
		t.Fatalf("first kind = %s", first.Kind)
	}
	second, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.Kind != KindResponse {
		t.Fatalf("second kind = %s", second.Kind)
	}
	if _, err := decoder.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next() error = %v, want EOF", err)
	}
}

func TestDecoderRejectsOversizedFrames(t *testing.T) {
	for _, suffix := range []string{"\n", ""} {
		decoder, err := NewDecoder(strings.NewReader(strings.Repeat("x", 17)+suffix), 16)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decoder.Next(); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Next() error = %v, want ErrFrameTooLarge", err)
		}
	}
}

func TestDecoderRejectsFrameLimitThatWouldOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if _, err := NewDecoder(strings.NewReader(""), maxInt); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("NewDecoder() error = %v, want overflow guard", err)
	}
}

func TestEncoderWritesOneValidatedFrame(t *testing.T) {
	var output bytes.Buffer
	encoder, err := NewEncoder(&output, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Write(map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{"clientName": "test"},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "{\"id\":1,\"method\":\"initialize\",\"params\":{\"clientName\":\"test\"}}\n"; got != want {
		t.Fatalf("encoded frame = %q, want %q", got, want)
	}
	if err := encoder.Write(map[string]any{"jsonrpc": "2.0", "method": "bad"}); err == nil {
		t.Fatal("Encoder.Write() accepted standard JSON-RPC envelope")
	}
}

func TestPeerReceiveHonorsContext(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	peer, err := NewPeer(reader, io.Discard, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := peer.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive() error = %v, want deadline exceeded", err)
	}

	if _, err := writer.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		t.Fatal(err)
	}
	receiveCtx, receiveCancel := context.WithTimeout(context.Background(), time.Second)
	defer receiveCancel()
	message, err := peer.Receive(receiveCtx)
	if err != nil {
		t.Fatal(err)
	}
	if message.Method != "initialized" {
		t.Fatalf("method = %q", message.Method)
	}
}

func TestPeerReceiveRejectsNilContext(t *testing.T) {
	peer, err := NewPeer(strings.NewReader(""), io.Discard, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Receive(nil); err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("Receive(nil) error = %v", err)
	}
}
