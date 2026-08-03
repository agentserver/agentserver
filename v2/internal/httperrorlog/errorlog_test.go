package httperrorlog

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewSuppressesOnlyLoopbackTLSHandshakeEOF(t *testing.T) {
	for _, message := range []string{
		"http: TLS handshake error from 127.0.0.1:43122: EOF",
		"http: TLS handshake error from [::1]:43122: EOF",
	} {
		var output bytes.Buffer
		New(&output).Print(message)
		if output.Len() != 0 {
			t.Fatalf("loopback probe error was logged: %q", output.String())
		}
	}
}

func TestNewRetainsActionableHTTPServerErrors(t *testing.T) {
	for _, message := range []string{
		"http: TLS handshake error from 192.0.2.10:43122: EOF",
		"http: TLS handshake error from 127.0.0.1:43122: tls: client didn't provide a certificate",
		"http: Accept error: accept tcp: use of closed network connection",
	} {
		var output bytes.Buffer
		New(&output).Print(message)
		if !strings.HasSuffix(output.String(), message+"\n") {
			t.Fatalf("actionable error %q was not retained: %q", message, output.String())
		}
	}
}

func TestNewRejectsMultilineLookalike(t *testing.T) {
	message := "first line\nhttp: TLS handshake error from 127.0.0.1:43122: EOF"
	var output bytes.Buffer
	New(&output).Print(message)
	if !strings.Contains(output.String(), message) {
		t.Fatalf("multiline error was suppressed: %q", output.String())
	}
}
