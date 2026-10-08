package coredb

import (
	"testing"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
)

func TestWithManagedSandboxCatalogAuthorizesEveryInstalledRegion(t *testing.T) {
	catalog, err := managedsandboxprofile.NewCatalog("sg", []managedsandboxprofile.Binding{
		{Region: managedsandboxprofile.RegionSG, EnvironmentID: "aaaaaaaa-1111-4444-8888-111111111111"},
		{Region: managedsandboxprofile.RegionCN, EnvironmentID: "bbbbbbbb-1111-4444-8888-111111111111"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := (&StateStore{}).WithManagedSandboxCatalog(catalog)
	if store.defaultManagedRegion() != managedsandboxprofile.RegionSG {
		t.Fatalf("default managed region = %q", store.defaultManagedRegion())
	}
	if len(store.managedProfileIDs) != 2 {
		t.Fatalf("managed profile IDs = %#v", store.managedProfileIDs)
	}
	seen := map[string]bool{}
	for _, id := range store.managedProfileIDs {
		seen[id] = true
	}
	if !seen["aaaaaaaa-1111-4444-8888-111111111111"] || !seen["bbbbbbbb-1111-4444-8888-111111111111"] {
		t.Fatalf("managed profile IDs = %#v", store.managedProfileIDs)
	}
}
