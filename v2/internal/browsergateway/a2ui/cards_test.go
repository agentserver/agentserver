package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandCardIsValidDisplayOnlyA2UIV09(t *testing.T) {
	operations := CommandCard("10000000-0000-4000-8000-000000000001", CommandView{
		Command: "go test ./...", Output: "ok", Status: "succeeded",
	})
	if err := ValidateOperations(operations); err != nil {
		t.Fatalf("ValidateOperations() error = %v", err)
	}
	raw, err := json.Marshal(operations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sendDataModel") {
		t.Fatalf("display-only card requested data model echo: %s", raw)
	}
	if !strings.Contains(string(raw), `"version":"v0.9"`) || !strings.Contains(string(raw), `"createSurface"`) {
		t.Fatalf("card is not A2UI v0.9 operations: %s", raw)
	}
}

func TestFileChangeCardHasOneRootAndStableReferences(t *testing.T) {
	operations := FileChangeCard("10000000-0000-4000-8000-000000000002", []FileChange{
		{Path: "a.go", Kind: "update", Diff: "@@ -1 +1 @@"},
		{Path: "b.go", Kind: "add", Diff: "+package b"},
	})
	if err := ValidateOperations(operations); err != nil {
		t.Fatalf("ValidateOperations() error = %v", err)
	}
	components := operations[1].UpdateComponents.Components
	roots := 0
	for _, component := range components {
		if component.ID == "root" {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("root components = %d", roots)
	}
}

func TestValidateOperationsRejectsUnsafeShapes(t *testing.T) {
	operations := CommandCard("event", CommandView{Command: "true", Status: "succeeded"})
	operations[0].CreateSurface.SendDataModel = true
	if err := ValidateOperations(operations); err == nil || !strings.Contains(err.Error(), "data-model echo") {
		t.Fatalf("data-model echo validation error = %v", err)
	}

	operations = CommandCard("event", CommandView{Command: "true", Status: "succeeded"})
	operations[1].UpdateComponents.Components[1].Children = append(
		operations[1].UpdateComponents.Components[1].Children, "missing",
	)
	if err := ValidateOperations(operations); err == nil || !strings.Contains(err.Error(), "unknown child") {
		t.Fatalf("unknown child validation error = %v", err)
	}
}

func TestApprovalCardIsDisplayOnlyAndBounded(t *testing.T) {
	operations := ApprovalCard("event-1", ApprovalView{
		ApprovalID: "80000000-0000-4000-8000-000000000008", ToolName: "shell",
		Status: "pending", ExpiresAt: "2026-07-31T12:10:00Z",
	})
	if err := ValidateOperations(operations); err != nil {
		t.Fatal(err)
	}
	if operations[0].CreateSurface == nil || operations[0].CreateSurface.SurfaceID != "approval-event-1" ||
		operations[2].UpdateDataModel == nil {
		t.Fatalf("approval operations = %+v", operations)
	}
}
