package trajectorycursor

import (
	"strings"
	"testing"
	"time"
)

const (
	testWorkspaceID = "10000000-0000-4000-8000-000000000001"
	testSessionID   = "20000000-0000-4000-8000-000000000002"
	testActorID     = "30000000-0000-4000-8000-000000000003"
	testRunID       = "40000000-0000-4000-8000-000000000004"
)

func TestCodecRoundTripsBoundPosition(t *testing.T) {
	codec, err := NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	want := Position{
		Scope: Scope{WorkspaceID: testWorkspaceID, SessionID: testSessionID, ActorID: testActorID},
		RunID: testRunID, RunCreatedAt: time.Date(2026, 8, 15, 1, 2, 3, 456000000, time.UTC),
		AnchorSeq: 17, Rank: 30, RecordID: "operation:40000000-0000-4000-8000-000000000004",
	}
	token, err := codec.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(token, want.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded position = %+v, want %+v", got, want)
	}
}

func TestCodecRejectsForgeryAndCrossScopeUse(t *testing.T) {
	codec, _ := NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	position := Position{
		Scope: Scope{WorkspaceID: testWorkspaceID, SessionID: testSessionID, ActorID: testActorID},
		RunID: testRunID, RunCreatedAt: time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
		AnchorSeq: 1, RecordID: "run:" + testRunID,
	}
	token, _ := codec.Encode(position)
	last := token[len(token)-1]
	replacement := byte('A')
	if last == replacement {
		replacement = 'B'
	}
	forged := token[:len(token)-1] + string(replacement)
	if _, err := codec.Decode(forged, position.Scope); err == nil {
		t.Fatal("forged cursor was accepted")
	}
	crossScope := position.Scope
	crossScope.SessionID = strings.Replace(testSessionID, "2", "5", 1)
	if _, err := codec.Decode(token, crossScope); err == nil {
		t.Fatal("cross-scope cursor was accepted")
	}
}
