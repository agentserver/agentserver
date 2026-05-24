package codexececdge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/clientmeta"
)

const registerBodyMax = 1 << 20 // 1 MiB

func (s *Server) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
	// 1MB cap: register payloads are auth JSON (<1KB typical); defensive.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, registerBodyMax))
	if err != nil {
		s.logger.Warn("registerproxy: body too large or read error", "err", err)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	upstreamURL := s.cfg.UpstreamBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	clientIP := clientmeta.ClientIP(r)
	deadline := time.Now().Add(s.cfg.RegisterRetryTotalTimeout)
	backoff := s.cfg.RegisterRetryInitialBackoff

	var (
		lastStatus int
		lastHeader http.Header
		lastBody   []byte
		lastErr    error
	)
	for {
		attemptCtx, attemptCancel := context.WithCancel(r.Context())
		req, _ := http.NewRequestWithContext(attemptCtx, r.Method, upstreamURL, bytes.NewReader(body))
		copyHeaders(req.Header, r.Header)
		req.Header.Set("X-Forwarded-For", clientIP)
		req.Header.Set("X-Real-IP", clientIP)

		resp, err := s.httpClient.Do(req)
		if err == nil && !isRetryableStatus(resp.StatusCode) {
			attemptCancel()
			writeUpstreamResponse(w, resp)
			return
		}

		// Retryable. Capture the response state (for possible exhaustion
		// passthrough) and release the connection.
		lastErr = err
		if resp != nil {
			lastStatus = resp.StatusCode
			lastHeader = resp.Header.Clone()
			lastBody, _ = io.ReadAll(io.LimitReader(resp.Body, registerBodyMax))
			_ = resp.Body.Close()
		} else {
			lastStatus = 0
			lastHeader = nil
			lastBody = nil
		}
		attemptCancel()

		sleep := backoff + jitter(backoff, 0.25)
		if time.Now().Add(sleep).After(deadline) {
			break
		}
		select {
		case <-time.After(sleep):
		case <-r.Context().Done():
			s.logger.Info("registerproxy: client canceled mid-retry")
			return
		}
		if backoff*2 > 8*time.Second {
			backoff = 8 * time.Second
		} else {
			backoff *= 2
		}
	}

	// Retry deadline exhausted. Surface the last upstream response if any,
	// otherwise return 502 with the dial error.
	if lastStatus == 0 {
		s.logger.Warn("registerproxy: retries exhausted (network)", "err", lastErr)
		http.Error(w, "upstream unreachable: "+lastErr.Error(), http.StatusBadGateway)
		return
	}
	s.logger.Warn("registerproxy: retries exhausted (status)", "status", lastStatus)
	for k, vs := range lastHeader {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(lastStatus)
	_, _ = w.Write(lastBody)
}

func isRetryableStatus(code int) bool {
	return code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

// copyHeaders copies all headers from src to dst except hop-by-hop.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		switch k {
		case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
			"Te", "Trailer", "Transfer-Encoding", "Upgrade",
			"X-Forwarded-For", "X-Real-IP":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeUpstreamResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	_ = resp.Body.Close()
}

// jitter returns a uniformly-distributed duration in [-frac*base, +frac*base].
// Uses crypto/rand so callers don't need to seed math/rand.
func jitter(base time.Duration, frac float64) time.Duration {
	if base <= 0 || frac <= 0 {
		return 0
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := binary.BigEndian.Uint64(b[:])
	// Map u to [-1.0, +1.0).
	f := float64(int64(u>>1)) / float64(1<<62) // [0, 2.0)
	f -= 1.0                                   // [-1.0, 1.0)
	return time.Duration(float64(base) * frac * f)
}
