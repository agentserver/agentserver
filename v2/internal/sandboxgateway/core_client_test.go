package sandboxgateway

import (
	"errors"
	"net/http"
	"testing"
)

func TestCoreClientRequiresCanonicalSecureOriginAndDisablesRedirects(t *testing.T) {
	for _, raw := range []string{
		"http://core.internal",
		"https://user@core.internal",
		"https://core.internal/base",
		"https://core.internal?query=1",
		"https://core.internal#fragment",
	} {
		if _, err := NewCoreClient(raw, http.DefaultClient); err == nil {
			t.Fatalf("NewCoreClient(%q) accepted an unsafe origin", raw)
		}
	}
	original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	client, err := NewCoreClient("https://core.internal/", original)
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL.Path != "" || client.httpClient == original {
		t.Fatalf("client did not normalize origin or copy HTTP client: %+v", client.baseURL)
	}
	if err := client.httpClient.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy = %v, want ErrUseLastResponse", err)
	}
	if _, err := NewCoreClient("http://[::1]:8080", original); err != nil {
		t.Fatalf("loopback cleartext should remain available for insecure development: %v", err)
	}
}
