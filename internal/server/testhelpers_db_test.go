package server

import (
	"os"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

// newTestServerTUI opens the integration DB and returns a *Server wired
// with it. Skips when TEST_DATABASE_URL is unset.
func newTestServerTUI(t *testing.T) (*Server, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{DB: d}, func() { d.Close() }
}
