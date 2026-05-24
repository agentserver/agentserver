package codexececdge

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg        Config
	upstream   *url.URL
	httpClient *http.Client
	logger     *slog.Logger
}

func NewServer(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.UpstreamBaseURL)
	if err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	return &Server{
		cfg:      cfg,
		upstream: u,
		httpClient: &http.Client{
			Timeout: 0, // per-attempt timeout enforced via per-try context
			Transport: &http.Transport{
				Proxy:               nil,
				DisableKeepAlives:   false,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 16,
			},
		},
		logger: logger,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/codex-exec/{exe_id}", s.handleWSProxy)
	r.Post("/cloud/executor/{exe_id}/register", s.handleRegisterProxy)
	r.Post("/cloud/environment/{env_id}/register", s.handleRegisterProxy)
	return r
}

// Placeholders — filled in by later tasks.
func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request)       { http.Error(w, "not implemented", 501) }
func (s *Server) handleRegisterProxy(w http.ResponseWriter, r *http.Request) { http.Error(w, "not implemented", 501) }
