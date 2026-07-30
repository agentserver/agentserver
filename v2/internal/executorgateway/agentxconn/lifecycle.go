package agentxconn

import (
	"bytes"
	"encoding/json"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

// InitializePayload constructs the only gateway-originated lifecycle request
// admitted by the Phase 1 profile.
func InitializePayload(requestID string) (Payload, error) {
	if err := validateText("initialize request id", requestID, 256); err != nil {
		return Payload{}, err
	}
	rpc, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			ProtocolVersion     string   `json:"protocolVersion"`
			ClientName          string   `json:"clientName"`
			OuterProfileVersion string   `json:"outerProfileVersion"`
			ProcessMethods      []string `json:"processMethods"`
		} `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "initialize",
		Params: struct {
			ProtocolVersion     string   `json:"protocolVersion"`
			ClientName          string   `json:"clientName"`
			OuterProfileVersion string   `json:"outerProfileVersion"`
			ProcessMethods      []string `json:"processMethods"`
		}{
			ProtocolVersion:     CurrentProtocolVersion,
			ClientName:          "agentserver-executor-gateway",
			OuterProfileVersion: execprofile.Version,
			ProcessMethods:      execprofile.ProcessMethods(),
		},
	})
	if err != nil {
		return Payload{}, err
	}
	return Payload{Type: MessageTypeLifecycle, RPC: rpc}, nil
}

// InitializedPayload constructs the lifecycle completion notification. It is
// sent only after a matching initialize response has been received.
func InitializedPayload() Payload {
	return Payload{
		Type: MessageTypeLifecycle,
		RPC:  json.RawMessage(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
	}
}

// ValidateInitializeResponse applies the normal direction/profile checks and
// also correlates the response ID with the gateway request. A JSON-RPC error
// is a valid frame but a terminal lifecycle negotiation failure.
func ValidateInitializeResponse(frame Frame, requestID string) error {
	if err := frame.ValidateForReceiver(RoleGateway); err != nil {
		return err
	}
	if frame.Type != MessageTypeLifecycle {
		return protocolError(ErrorMethodNotNegotiated, true, "initialize response arrived in %q frame", frame.Type)
	}
	rpc, err := parseStandardRPC(frame.RPC)
	if err != nil {
		return err
	}
	wantID, err := json.Marshal(requestID)
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(rpc.ID), wantID) {
		return protocolError(ErrorSequenceConflict, true, "initialize response id does not match the pending request")
	}
	if rpc.Kind == standardRPCError {
		return protocolError(ErrorProtocolVersionUnsupported, true, "agentx rejected lifecycle initialization")
	}
	if rpc.Kind != standardRPCResponse {
		return protocolError(ErrorMethodNotNegotiated, true, "agentx lifecycle message is not an initialize response")
	}
	return nil
}
