package coredb

import "testing"

func TestValidateProductionBootstrapRequiresHTTPSIdentityAndCanonicalIDs(t *testing.T) {
	valid := validProductionBootstrap()
	if err := validateProductionBootstrap(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ProductionBootstrap){
		"workspace":      func(value *ProductionBootstrap) { value.WorkspaceID = "not-a-uuid" },
		"issuer":         func(value *ProductionBootstrap) { value.ExternalOIDCIssuer = "http://idp.example.test" },
		"trailing slash": func(value *ProductionBootstrap) { value.ExternalOIDCIssuer += "/" },
		"subject":        func(value *ProductionBootstrap) { value.ExternalOIDCSubject = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := validateProductionBootstrap(value); err == nil {
				t.Fatal("invalid production bootstrap was accepted")
			}
		})
	}
}

func validProductionBootstrap() ProductionBootstrap {
	return ProductionBootstrap{
		WorkspaceID:         "40000000-0000-4000-8000-000000000004",
		SessionID:           "50000000-0000-4000-8000-000000000005",
		UserID:              "10000000-0000-4000-8000-000000000001",
		ExternalOIDCIssuer:  "https://idp.example.test/oidc",
		ExternalOIDCSubject: "production-owner",
		ExecutorID:          "20000000-0000-4000-8000-000000000002",
	}
}
