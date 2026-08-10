package coredb

import "testing"

func TestPlatformWorkspaceMutationsRejectDeploymentOrImplicitCredentialMode(t *testing.T) {
	store := &StateStore{}
	for _, mode := range []string{"", "global", "auto", "WEBHOOK_SWAP"} {
		t.Run("create_"+mode, func(t *testing.T) {
			_, err := store.CreatePlatformWorkspace(t.Context(), CreatePlatformWorkspaceCommand{
				WorkspaceID: "81000000-0000-4000-8000-000000000011",
				ActorID:     "82000000-0000-4000-8000-000000000012",
				Name:        "SG", ManagedLarkCredentialMode: mode,
			})
			if !HasStateErrorCode(err, ErrorInvalidArgument) {
				t.Fatalf("CreatePlatformWorkspace(%q) error = %v", mode, err)
			}
		})
		t.Run("update_"+mode, func(t *testing.T) {
			_, err := store.UpdatePlatformWorkspace(t.Context(), UpdatePlatformWorkspaceCommand{
				WorkspaceID: "81000000-0000-4000-8000-000000000011",
				ActorID:     "82000000-0000-4000-8000-000000000012",
				Name:        "SG", ManagedLarkCredentialMode: mode, ExpectedVersion: 1,
			})
			if !HasStateErrorCode(err, ErrorInvalidArgument) {
				t.Fatalf("UpdatePlatformWorkspace(%q) error = %v", mode, err)
			}
		})
	}
}
