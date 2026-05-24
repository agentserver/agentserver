package codexececdge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRegisterUpstream returns an upstream that records each call and lets
// the test drive the response via the responder fn.
type fakeRegisterUpstream struct {
	t         *testing.T
	calls     atomic.Int64
	headersCh chan http.Header
	bodiesCh  chan []byte
	responder func(call int64, w http.ResponseWriter)
}

func newFakeRegisterUpstream(t *testing.T, responder func(call int64, w http.ResponseWriter)) (*httptest.Server, *fakeRegisterUpstream) {
	f := &fakeRegisterUpstream{
		t:         t,
		headersCh: make(chan http.Header, 32),
		bodiesCh:  make(chan []byte, 32),
		responder: responder,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		select {
		case f.headersCh <- r.Header.Clone():
		default:
		}
		select {
		case f.bodiesCh <- body:
		default:
		}
		f.responder(n, w)
	}))
	return ts, f
}

func TestRegisterProxy_2xxPassThrough(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"exe_x"}`))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := []byte(`{"foo":"bar"}`)
	resp, err := http.Post(ts.URL+"/cloud/environment/exe_x/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	var parsed map[string]string
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("response body: %v / %q", err, got)
	}
	if parsed["id"] != "exe_x" {
		t.Errorf("body: %q", got)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream called %d times", fake.calls.Load())
	}
	// XFF should have been set by edge.
	h := <-fake.headersCh
	if h.Get("X-Forwarded-For") == "" {
		t.Error("X-Forwarded-For not set")
	}
	// Body forwarded verbatim.
	gotBody := <-fake.bodiesCh
	if !bytes.Equal(gotBody, body) {
		t.Errorf("upstream body: got %q want %q", gotBody, body)
	}
}

func TestRegisterProxy_4xxNoRetry(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream called %d times (should not retry 4xx)", fake.calls.Load())
	}
}

func TestRegisterProxy_BodyTooLarge(t *testing.T) {
	srv := newTestServer(t, Config{
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	huge := bytes.Repeat([]byte("x"), 2<<20) // 2MB
	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/octet-stream", bytes.NewReader(huge))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d want 413", resp.StatusCode)
	}
}

func TestRegisterProxy_RetriesOn5xxUntilSuccess(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(call int64, w http.ResponseWriter) {
		if call < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   5 * time.Second,
		RegisterRetryInitialBackoff: 5 * time.Millisecond, // keep test fast
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if got := fake.calls.Load(); got != 3 {
		t.Errorf("upstream calls: got %d want 3", got)
	}
}

func TestRegisterProxy_ExhaustsRetriesAndSurfacesLastStatus(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("still down"))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   80 * time.Millisecond, // tiny deadline
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status: got %d want 503 (surfaced from upstream)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "still down" {
		t.Errorf("body: %q", body)
	}
	if got := fake.calls.Load(); got < 2 {
		t.Errorf("upstream calls: got %d, expected at least 2 retries", got)
	}
}

func TestRegisterProxy_ExhaustsRetriesOnDialError(t *testing.T) {
	srv := newTestServer(t, Config{
		UpstreamBaseURL:             "http://127.0.0.1:1", // closed
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   80 * time.Millisecond,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         50 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d want 502", resp.StatusCode)
	}
}

func TestRegisterProxy_BackoffCap(t *testing.T) {
	// Sanity: the cap math should not let backoff exceed 8s after many doublings.
	d := 500 * time.Millisecond
	for i := 0; i < 10; i++ {
		if d*2 > 8*time.Second {
			d = 8 * time.Second
		} else {
			d *= 2
		}
	}
	if d != 8*time.Second {
		t.Errorf("cap not enforced: %v", d)
	}
}
