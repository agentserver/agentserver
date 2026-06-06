package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const agentCardsCaptureDriverName = "agent_cards_capture"

var (
	registerAgentCardsCaptureDriver sync.Once
	agentCardsCapture               struct {
		sync.Mutex
		query string
	}
)

type agentCardsDriver struct{}

func (agentCardsDriver) Open(string) (driver.Conn, error) {
	return agentCardsConn{}, nil
}

type agentCardsConn struct{}

func (agentCardsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare should not be called")
}

func (agentCardsConn) Close() error { return nil }

func (agentCardsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("Begin should not be called")
}

func (agentCardsConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	agentCardsCapture.Lock()
	defer agentCardsCapture.Unlock()
	agentCardsCapture.query = query
	return driver.RowsAffected(1), nil
}

func TestUpsertAgentCardFromCapabilitiesRestoresAvailableOnExistingCard(t *testing.T) {
	registerAgentCardsCaptureDriver.Do(func() {
		sql.Register(agentCardsCaptureDriverName, agentCardsDriver{})
	})

	agentCardsCapture.Lock()
	agentCardsCapture.query = ""
	agentCardsCapture.Unlock()

	sqlDB, err := sql.Open(agentCardsCaptureDriverName, "")
	if err != nil {
		t.Fatalf("open capture db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	d := &DB{DB: sqlDB}
	if err := d.UpsertAgentCardFromCapabilities("sbx_1", "ws_1", "agent", []byte(`{}`)); err != nil {
		t.Fatalf("upsert from capabilities: %v", err)
	}

	agentCardsCapture.Lock()
	query := agentCardsCapture.query
	agentCardsCapture.Unlock()

	if !strings.Contains(query, "agent_status = 'available'") {
		t.Fatalf("conflict update did not restore agent_status to available:\n%s", query)
	}
}

func TestUpsertAgentCardFromCapabilitiesRestoresAvailableInPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := Open(url)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workspaceID := "ws_card_restore_" + suffix
	userID := "u_card_restore_" + suffix
	sandboxID := "sbx_card_restore_" + suffix
	proxyToken := "proxy_card_restore_" + suffix
	tunnelToken := "tunnel_card_restore_" + suffix

	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM agent_cards WHERE sandbox_id = $1`, sandboxID)
		_, _ = d.Exec(`DELETE FROM proxy_tokens WHERE token IN ($1, $2)`, proxyToken, tunnelToken)
		_, _ = d.Exec(`DELETE FROM sandboxes WHERE id = $1`, sandboxID)
		_, _ = d.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1 OR user_id = $2`, workspaceID, userID)
		_, _ = d.Exec(`DELETE FROM workspaces WHERE id = $1`, workspaceID)
		_, _ = d.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := d.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, 'card restore test')`, workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO users (id, email) VALUES ($1, $2)`, userID, userID+"@test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')`, workspaceID, userID); err != nil {
		t.Fatalf("insert workspace member: %v", err)
	}
	if err := d.CreateLocalSandbox(sandboxID, workspaceID, userID, "card-restore-agent", "custom", "", proxyToken, tunnelToken, "cr"+suffix[len(suffix)-8:]); err != nil {
		t.Fatalf("create local sandbox: %v", err)
	}
	if err := d.UpsertAgentCard(&AgentCard{
		SandboxID:   sandboxID,
		WorkspaceID: workspaceID,
		AgentType:   "custom",
		DisplayName: "card-restore-agent",
		CardJSON:    json.RawMessage(`{"old":true}`),
		AgentStatus: "offline",
	}); err != nil {
		t.Fatalf("upsert offline card: %v", err)
	}

	if err := d.UpsertAgentCardFromCapabilities(sandboxID, workspaceID, "card-restore-agent", json.RawMessage(`{"restored":true}`)); err != nil {
		t.Fatalf("upsert card from capabilities: %v", err)
	}

	card, err := d.GetAgentCard(sandboxID)
	if err != nil {
		t.Fatalf("get agent card: %v", err)
	}
	if card == nil {
		t.Fatal("agent card missing")
	}
	if card.AgentStatus != "available" {
		t.Fatalf("agent card status = %q, want available", card.AgentStatus)
	}
}
