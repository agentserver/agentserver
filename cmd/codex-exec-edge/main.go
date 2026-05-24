package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/internal/codexececdge"
	"github.com/agentserver/agentserver/internal/wsbridge"
)

func main() {
	cfg, err := codexececdge.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	srv, err := codexececdge.NewServer(cfg)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("codex-exec-edge listening on :%s, upstream=%s", cfg.Port, cfg.UpstreamBaseURL)
	ln, err := wsbridge.ListenWithKeepAlive(context.Background(), "tcp", ":"+cfg.Port)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
