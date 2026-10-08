package coreserver

import (
	"encoding/json"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestManagedNotSentEvidence(t *testing.T) {
	base := map[string]any{"kind": "process_start", "status": "failed", "dispatchOutcome": "not_sent", "reasonCode": "credential_unauthorized", "acknowledged": false, "outputComplete": true, "exitCode": nil}
	for _, test := range []struct {
		field string
		value any
	}{
		{"", nil}, {"dispatchOutcome", "unknown"}, {"status", "succeeded"}, {"acknowledged", true}, {"acknowledged", nil}, {"outputComplete", false}, {"kind", "read_file"}, {"exitCode", 0}, {"reasonCode", "secret-value"},
	} {
		t.Run(test.field, func(t *testing.T) {
			data := make(map[string]any)
			for key, value := range base {
				data[key] = value
			}
			if test.field != "" {
				data[test.field] = test.value
			}
			raw, _ := json.Marshal(data)
			got, err := managedProcessNotSentEvidence("failed", raw)
			if test.field == "" {
				if err != nil || !got {
					t.Fatalf("valid evidence: %v", err)
				}
				store := &recordingExecutionStateStore{}
				_, err = (StateStoreExecutionCommands{Store: store}).CompleteOperation(t.Context(), corecontract.CompleteOperationRequest{TerminalStatus: "failed", Result: raw})
				if err != nil || !store.completeOperation.DispatchNotSent {
					t.Fatalf("not-sent evidence lost: %v", err)
				}
			} else if err == nil || got {
				t.Fatal("invalid not-sent evidence accepted")
			}
		})
	}
	if got, err := managedProcessNotSentEvidence("unknown", json.RawMessage(`{"status":"unknown"}`)); err != nil || got {
		t.Fatal("legacy unknown changed")
	}
}
