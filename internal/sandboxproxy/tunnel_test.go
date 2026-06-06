package sandboxproxy

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/sbxstore"
	"github.com/agentserver/agentserver/internal/tunnel"
	"nhooyr.io/websocket"
)

func openTunnelTestDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestTunnelDisconnectMarksAgentCardOffline(t *testing.T) {
	d := openTunnelTestDB(t)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workspaceID := "ws_tunnel_card_" + suffix
	userID := "u_tunnel_card_" + suffix
	sandboxID := "sbx_tunnel_card_" + suffix
	proxyToken := "proxy_tunnel_card_" + suffix
	tunnelToken := "tunnel_tunnel_card_" + suffix

	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM agent_cards WHERE sandbox_id = $1`, sandboxID)
		_, _ = d.Exec(`DELETE FROM proxy_tokens WHERE token IN ($1, $2)`, proxyToken, tunnelToken)
		_, _ = d.Exec(`DELETE FROM sandboxes WHERE id = $1`, sandboxID)
		_, _ = d.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1 OR user_id = $2`, workspaceID, userID)
		_, _ = d.Exec(`DELETE FROM workspaces WHERE id = $1`, workspaceID)
		_, _ = d.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := d.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, 'tunnel card test')`, workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO users (id, email) VALUES ($1, $2)`, userID, userID+"@test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')`, workspaceID, userID); err != nil {
		t.Fatalf("insert workspace member: %v", err)
	}
	if err := d.CreateLocalSandbox(sandboxID, workspaceID, userID, "tunnel-card-agent", "custom", "", proxyToken, tunnelToken, "tc"+suffix[len(suffix)-8:]); err != nil {
		t.Fatalf("create local sandbox: %v", err)
	}
	if err := d.UpsertAgentCard(&db.AgentCard{
		SandboxID:   sandboxID,
		WorkspaceID: workspaceID,
		AgentType:   "custom",
		DisplayName: "tunnel-card-agent",
		CardJSON:    json.RawMessage(`{}`),
		AgentStatus: "available",
	}); err != nil {
		t.Fatalf("upsert agent card: %v", err)
	}

	srv := New(Config{}, nil, d, sbxstore.NewStore(d), tunnel.NewRegistry(), nil)
	httpSrv := httptest.NewServer(srv.Router())
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/api/tunnel/" + sandboxID + "?token=" + tunnelToken
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "test disconnect")

	deadline := time.Now().Add(3 * time.Second)
	for {
		sbx, err := d.GetSandbox(sandboxID)
		if err != nil {
			t.Fatalf("get sandbox: %v", err)
		}
		if sbx != nil && sbx.Status == sbxstore.StatusOffline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox did not go offline after tunnel disconnect; got status %q", sbx.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}

	for {
		card, err := d.GetAgentCard(sandboxID)
		if err != nil {
			t.Fatalf("get agent card: %v", err)
		}
		if card == nil {
			t.Fatal("agent card missing")
		}
		if card.AgentStatus == "offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent card status = %q, want offline after active tunnel disconnect", card.AgentStatus)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
