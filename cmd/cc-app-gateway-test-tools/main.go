// Package main provides fake services for cc-app-gateway integration tests.
//
// Subcommands:
//   - fake-agentserver: serves POST /internal/workspace-token and GET /healthz
//   - fake-llmproxy: speaks Anthropic Messages API and returns a canned reply
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cc-app-gateway-test-tools <subcommand> [flags]\n")
		fmt.Fprintf(os.Stderr, "subcommands: fake-agentserver, fake-llmproxy\n")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fake-agentserver":
		runFakeAgentserver(os.Args[2:])
	case "fake-llmproxy":
		runFakeLLMProxy(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(2)
	}
}

// ─── fake-agentserver ────────────────────────────────────────────────────────

func runFakeAgentserver(args []string) {
	fs := flag.NewFlagSet("fake-agentserver", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "address to listen on")
	wsToken := fs.String("workspace-token", "deadbeef", "workspace token to return")
	logRequests := fs.Bool("log-requests", true, "log every inbound request to stderr")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	mux := http.NewServeMux()

	// GET /healthz — liveness probe used by docker-compose healthcheck.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if *logRequests {
			log.Printf("[fake-agentserver] %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	// POST /internal/workspace-token — returns the configured token regardless of input.
	mux.HandleFunc("POST /internal/workspace-token", func(w http.ResponseWriter, r *http.Request) {
		if *logRequests {
			log.Printf("[fake-agentserver] %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": *wsToken}) //nolint:errcheck
	})

	// Catch-all: log and 404 so unexpected calls surface clearly.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-agentserver] UNMATCHED %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	log.Printf("[fake-agentserver] listening on %s (workspace-token=%s)", *listen, *wsToken)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatalf("[fake-agentserver] ListenAndServe: %v", err)
	}
}

// ─── fake-llmproxy ───────────────────────────────────────────────────────────

// anthropicResponse is the minimum Anthropic Messages API response shape.
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Model      string             `json:"model"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func runFakeLLMProxy(args []string) {
	fs := flag.NewFlagSet("fake-llmproxy", flag.ExitOnError)
	listen := fs.String("listen", ":8081", "address to listen on")
	acceptToken := fs.String("accept-token", "deadbeef", "bearer token to accept")
	cannedReply := fs.String("canned-reply", "pong", "text to return in content[0].text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	mux := http.NewServeMux()

	// GET /api/hello — OAuth hello endpoint.
	mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-llmproxy] GET /api/hello")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true}) //nolint:errcheck
	})

	// GET /v1/oauth/hello — OAuth hello endpoint.
	mux.HandleFunc("GET /v1/oauth/hello", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-llmproxy] GET /v1/oauth/hello")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true}) //nolint:errcheck
	})

	// POST /api/event_logging/v2/batch — telemetry sink.
	mux.HandleFunc("POST /api/event_logging/v2/batch", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-llmproxy] POST /api/event_logging/v2/batch")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true}) //nolint:errcheck
	})

	// GET /v1/environment_providers — environment providers stub.
	mux.HandleFunc("GET /v1/environment_providers", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-llmproxy] GET /v1/environment_providers")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"providers": []interface{}{}}) //nolint:errcheck
	})

	// POST /v1/messages — main Anthropic Messages API endpoint.
	// claude sends Authorization: Bearer <token>; we verify it matches --accept-token.
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[fake-llmproxy] POST /v1/messages (query=%s)", r.URL.RawQuery)

		// Verify bearer token. Don't log either side of the comparison —
		// CodeQL flags any logging of header-derived data as clear-text
		// secret logging, and even in a test fixture (Bearer is "deadbeef")
		// the lesson generalizes: never log auth headers.
		authHeader := r.Header.Get("Authorization")
		want := "Bearer " + *acceptToken
		if authHeader != want {
			log.Printf("[fake-llmproxy] auth FAIL: header mismatch (got %d bytes, want %d bytes)", len(authHeader), len(want))
			http.Error(w, `{"error":{"type":"authentication_error","message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}

		resp := anthropicResponse{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			// Use the same model field claude sent, or fallback to haiku.
			Model: "claude-haiku-4-5",
			Content: []anthropicContent{
				{Type: "text", Text: *cannedReply},
			},
			StopReason: "end_turn",
			Usage:      anthropicUsage{InputTokens: 10, OutputTokens: 5},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})

	// Catch-all: serve 200 {} for HEAD, /api/*, /v1/* and anything else claude calls.
	// This prevents claude's OAuth/telemetry pre-flight requests from blocking the run.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		log.Printf("[fake-llmproxy] catch-all: %s %s", r.Method, path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		fmt.Fprint(w, "{}")
	})

	log.Printf("[fake-llmproxy] listening on %s (accept-token=%s, canned-reply=%q)", *listen, *acceptToken, *cannedReply)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatalf("[fake-llmproxy] ListenAndServe: %v", err)
	}
}
