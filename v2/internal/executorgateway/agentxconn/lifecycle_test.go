package agentxconn

import (
	"encoding/json"
	"testing"
)

func TestLifecycleHelpersCorrelateInitializeResponse(t *testing.T) {
	payload, err := InitializePayload("init-1")
	if err != nil {
		t.Fatal(err)
	}
	request := Frame{
		Type:       MessageTypeLifecycle,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Generation: 7,
		RPC:        payload.RPC,
	}
	if err := request.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("initialize request error = %v", err)
	}
	response := Frame{
		Type:       MessageTypeLifecycle,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Ack:        1,
		Generation: 7,
		RPC:        json.RawMessage(`{"jsonrpc":"2.0","id":"init-1","result":{"sessionId":"30000000-0000-0000-0000-000000000003","protocolVersion":"2.0","serverName":"agentx","outerProfileVersion":"process-v1","processMethods":["process/start","process/read","process/write","process/terminate"]}}`),
	}
	if err := ValidateInitializeResponse(response, "init-1"); err != nil {
		t.Fatalf("ValidateInitializeResponse() error = %v", err)
	}
	if err := ValidateInitializeResponse(response, "other"); codeOf(err) != ErrorSequenceConflict {
		t.Fatalf("mismatched response ID error = %v", err)
	}
	initialized := InitializedPayload()
	frame := Frame{Type: initialized.Type, SessionID: testSessionID, SessionSeq: 2, Ack: 1, Generation: 7, RPC: initialized.RPC}
	if err := frame.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("initialized notification error = %v", err)
	}
}

func TestSessionErrorFromPreservesExactGap(t *testing.T) {
	err := gapProtocolError(ErrorResumeGap, true, 4, 7, "missing replay range")
	value := SessionErrorFrom(err)
	if value.LostFrom == nil || value.LostTo == nil || *value.LostFrom != 4 || *value.LostTo != 7 {
		t.Fatalf("SessionErrorFrom() = %+v", value)
	}
	if _, err := Encode(value, testWireLimits()); err != nil {
		t.Fatalf("gap session error does not pass wire validator: %v", err)
	}
}
