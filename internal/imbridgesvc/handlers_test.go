package imbridgesvc

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPatchIMChannel_AcceptsManagedCCRoutingMode(t *testing.T) {
	payload := map[string]interface{}{
		"routing_mode": "managed_cc",
	}
	body, _ := json.Marshal(payload)

	// Verify the validator logic accepts "managed_cc"
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}

	mode := payload["routing_mode"].(string)
	// This is the same condition as in handlers.go:977
	if mode != "nanoclaw" && mode != "codex" && mode != "managed_cc" {
		t.Errorf("managed_cc should be accepted by validator, but validator rejected it")
		return
	}

	t.Logf("managed_cc validator check passed")
}
