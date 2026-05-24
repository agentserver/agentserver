package codexececdge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/clientmeta"
	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/agentserver/agentserver/internal/wsbridge"
	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"
)

func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request) {
	exeID := chi.URLParam(r, "exe_id")
	token := r.URL.Query().Get("token")
	if exeID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusUnauthorized)
		return
	}
	if err := wsticket.Verify(token, exeID, s.cfg.AgentserverInternalSecret); err != nil {
		s.logger.Warn("wsproxy: bad ticket", "exe_id", exeID, "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 1. Dial upstream BEFORE accepting the client ws — so we can return
	//    a plain HTTP 502 if the upstream is unreachable, without doing a
	//    pointless ws upgrade on the client side.
	upstreamURL := s.buildUpstreamWSURL(exeID, token)
	dialCtx, dialCancel := context.WithTimeout(r.Context(), s.cfg.UpstreamDialTimeout)
	defer dialCancel()
	clientIP := clientmeta.ClientIP(r)
	dialHdr := http.Header{}
	dialHdr.Set("X-Forwarded-For", clientIP)
	dialHdr.Set("X-Real-IP", clientIP)
	if ua := r.Header.Get("User-Agent"); ua != "" {
		dialHdr.Set("User-Agent", ua)
	}
	upstream, _, err := websocket.Dial(dialCtx, upstreamURL, &websocket.DialOptions{
		HTTPHeader: dialHdr,
	})
	if err != nil {
		s.logger.Warn("wsproxy: upstream dial failed", "exe_id", exeID, "err", err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	upstream.SetReadLimit(-1)

	// 2. Upgrade the client side.
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Warn("wsproxy: client accept failed", "exe_id", exeID, "err", err)
		_ = upstream.Close(websocket.StatusInternalError, "client accept failed")
		return
	}
	client.SetReadLimit(-1)

	// 3. Two pump goroutines + keepalive on both sides.
	pumpCtx, pumpCancel := context.WithCancel(r.Context())
	defer pumpCancel()
	go wsbridge.KeepAlive(pumpCtx, client, 30*time.Second)
	go wsbridge.KeepAlive(pumpCtx, upstream, 30*time.Second)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := pump(pumpCtx, client, upstream)
		pumpCancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("wsproxy: client→upstream pump error", "exe_id", exeID, "err", err)
		}
		closeOther(upstream, err)
	}()
	go func() {
		defer wg.Done()
		err := pump(pumpCtx, upstream, client)
		pumpCancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("wsproxy: upstream→client pump error", "exe_id", exeID, "err", err)
		}
		closeOther(client, err)
	}()
	wg.Wait()
	s.logger.Info("wsproxy: closed", "exe_id", exeID)
}

// buildUpstreamWSURL converts UpstreamBaseURL (http/https) into a ws/wss URL
// and appends the codex-exec path + token.
func (s *Server) buildUpstreamWSURL(exeID, token string) string {
	u := *s.upstream
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = "/codex-exec/" + url.PathEscape(exeID)
	q := url.Values{}
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

// pump reads from src and writes each frame to dst until either side errors.
func pump(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		mt, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, mt, data); err != nil {
			return err
		}
	}
}

// closeOther forwards an appropriate close to the other side based on
// the originating error's WS close status (defaulting to 1011).
func closeOther(other *websocket.Conn, srcErr error) {
	if errors.Is(srcErr, context.Canceled) {
		_ = other.Close(websocket.StatusGoingAway, "")
		return
	}
	status := websocket.CloseStatus(srcErr)
	if status == -1 {
		status = websocket.StatusInternalError
	}
	_ = other.Close(status, "")
}
