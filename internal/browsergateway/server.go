package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// Server hosts the AG-UI endpoint. It is a ws client of codex-app-gateway.
type Server struct {
	cfg    ServeConfig
	logger *slog.Logger
	sse    *sse.SSEWriter
	dial   dialFunc
}

// NewServer builds a Server. The default dialer connects to
// cfg.CodexAppGatewayWSURL + "/codex-app/ws" forwarding the request's Bearer.
func NewServer(cfg ServeConfig, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	wsURL := strings.TrimRight(cfg.CodexAppGatewayWSURL, "/") + "/codex-app/ws"
	s := &Server{
		cfg:    cfg,
		logger: logger,
		sse:    sse.NewSSEWriter().WithLogger(logger),
	}
	s.dial = func(ctx context.Context, bearer string) (codexConn, error) {
		return codexclient.Dial(ctx, wsURL, bearer)
	}
	return s
}

// Handler returns the HTTP handler (routes + CORS).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /agui", s.handleAGUI)
	return s.withCORS(mux)
}

func (s *Server) handleAGUI(w http.ResponseWriter, r *http.Request) {
	bearer := extractBearer(r)
	if bearer == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	var in types.RunAgentInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid RunAgentInput: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("browser-gateway: run panicked", "err", rec)
		}
	}()
	runAGUI(r.Context(), w, s.sse, &in, bearer, s.dial)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	origin := "*"
	if len(s.cfg.AllowedOrigins) > 0 {
		origin = strings.Join(s.cfg.AllowedOrigins, ", ")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Cache-Control")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
