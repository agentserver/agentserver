package managedsandboxprofile

import (
	"strings"
	"testing"
)

func TestCatalogResolvesImmutableRegionBindings(t *testing.T) {
	bindings := []Binding{
		{Region: RegionCN, ProfileID: "tae-cn-v1", BindingSHA256: strings.Repeat("a", 64), EnvironmentID: "10000000-0000-4000-8000-000000000001"},
		{Region: RegionI18NTT, ProfileID: "tae-i18n-tt-v1", BindingSHA256: strings.Repeat("b", 64), EnvironmentID: "20000000-0000-4000-8000-000000000002"},
	}
	catalog, err := NewCatalog(RegionI18NTT, bindings)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	resolved, ok := catalog.Resolve(RegionCN)
	if !ok || resolved != bindings[0] {
		t.Fatalf("Resolve(cn) = %+v, %v", resolved, ok)
	}
	if _, ok := catalog.Resolve(RegionBOE); ok {
		t.Fatal("unconfigured BOE profile resolved")
	}
	copy := catalog.Bindings()
	copy[0].ProfileID = "changed"
	again, _ := catalog.Resolve(RegionCN)
	if again.ProfileID != bindings[0].ProfileID {
		t.Fatal("catalog binding was mutable through Bindings")
	}
}

func TestCatalogRejectsDuplicateEnvironment(t *testing.T) {
	environmentID := "10000000-0000-4000-8000-000000000001"
	_, err := NewCatalog(RegionI18NTT, []Binding{
		{Region: RegionI18NTT, ProfileID: "tae-i18n-tt-v1", BindingSHA256: strings.Repeat("a", 64), EnvironmentID: environmentID},
		{Region: RegionBOE, ProfileID: "tae-boe-v1", BindingSHA256: strings.Repeat("b", 64), EnvironmentID: environmentID},
	})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("NewCatalog() error = %v, want duplicate environment", err)
	}
}

func TestCatalogRequiresWorkspaceInitialRegion(t *testing.T) {
	_, err := NewCatalog(RegionCN, []Binding{{
		Region: RegionCN, ProfileID: "tae-cn-v1", BindingSHA256: strings.Repeat("a", 64),
		EnvironmentID: "10000000-0000-4000-8000-000000000001",
	}})
	if err == nil || !strings.Contains(err.Error(), DefaultRegion) {
		t.Fatalf("NewCatalog() error = %v, want fixed default region", err)
	}
}

func TestCatalogBindingsUseStablePublicRegionOrder(t *testing.T) {
	bindings := []Binding{
		{Region: RegionI18NTT, ProfileID: "tae-i18n-tt-v1", BindingSHA256: strings.Repeat("d", 64), EnvironmentID: "40000000-0000-4000-8000-000000000004"},
		{Region: RegionBOE, ProfileID: "tae-boe-v1", BindingSHA256: strings.Repeat("b", 64), EnvironmentID: "20000000-0000-4000-8000-000000000002"},
		{Region: RegionCN, ProfileID: "tae-cn-v1", BindingSHA256: strings.Repeat("a", 64), EnvironmentID: "10000000-0000-4000-8000-000000000001"},
		{Region: RegionI18NBD, ProfileID: "tae-i18n-bd-v1", BindingSHA256: strings.Repeat("c", 64), EnvironmentID: "30000000-0000-4000-8000-000000000003"},
	}
	catalog, err := NewCatalog(RegionI18NTT, bindings)
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Bindings()
	for index, region := range Regions() {
		if got[index].Region != region {
			t.Fatalf("Bindings()[%d].Region = %q, want %q", index, got[index].Region, region)
		}
	}
}
