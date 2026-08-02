package publichttps

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateURLUsesClosedPublicHTTPSProfile(t *testing.T) {
	valid, err := ValidateURL("https://gateway.example.com/v1/responses", "/v1/responses")
	if err != nil || valid.String() != "https://gateway.example.com/v1/responses" {
		t.Fatalf("valid public URL = %v, %v", valid, err)
	}
	for _, raw := range []string{
		"http://gateway.example.com/v1/responses",
		"https://gateway.example.com:8443/v1/responses",
		"https://user@gateway.example.com/v1/responses",
		"https://127.0.0.1/v1/responses",
		"https://Gateway.example.com/v1/responses",
		"https://gateway.example.com/v1/responses?key=value",
		"https://gateway.example.com/v1/other",
	} {
		if _, err := ValidateURL(raw, "/v1/responses"); err == nil {
			t.Fatalf("unsafe public URL %q was accepted", raw)
		}
	}
}

func TestIsPublicAddressRejectsPrivateAndSpecialUseRanges(t *testing.T) {
	for _, raw := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.0.2.1", "192.31.196.1", "192.52.193.1", "192.168.1.1", "192.175.48.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "::1", "64:ff9b:1::1",
		"::192.168.1.1", "64:ff9b::a9fe:a9fe", "100::1", "2001:db8::1",
		"2620:4f:8000::1", "3fff::1", "5f00::1", "fc00::1", "fe80::1", "ff02::1",
	} {
		if IsPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("special-use address %s was accepted", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("public address %s was rejected", raw)
		}
	}
}

func TestControlledDialRejectsMixedDNSAnswerBeforeConnecting(t *testing.T) {
	dialer := &recordingDialer{}
	client, err := NewClient(ClientConfig{
		Resolver: staticResolver{addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1"),
		}},
		Dialer: dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Transport == nil {
		t.Fatal("public HTTPS client has no transport")
	}
	// Exercise the same dial closure through a direct construction so no real
	// TLS or network operation is needed.
	_, err = controlledDialContext(staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1"),
	}}, dialer)(t.Context(), "tcp", "gateway.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "non-public") || len(dialer.addresses) != 0 {
		t.Fatalf("mixed DNS dial = %v, attempts %v", err, dialer.addresses)
	}
}

type staticResolver struct {
	addresses []netip.Addr
	err       error
}

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), resolver.err
}

type recordingDialer struct {
	addresses []string
}

func (dialer *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	dialer.addresses = append(dialer.addresses, "called")
	return nil, errors.New("unreachable")
}
