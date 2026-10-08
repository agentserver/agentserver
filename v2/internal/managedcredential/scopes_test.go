package managedcredential

import "testing"

func TestScopeBindings(t *testing.T) {
	const sg = "aaaaaaaa-1111-4444-8888-111111111111"
	const cn = "bbbbbbbb-1111-4444-8888-111111111111"
	raw := `[{"environmentId":"` + sg + `","scope":"sg-managed-cli"},{"environmentId":"` + cn + `","scope":"cn-managed-cli"}]`
	bindings, err := ParseScopeBindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	for env, want := range map[string]string{sg: "sg-managed-cli", cn: "cn-managed-cli"} {
		if got, ok := bindings.Scope(env); !ok || got != want {
			t.Fatalf("scope %s = %q, %v", env, got, ok)
		}
	}
	if _, ok := bindings.Scope("unknown"); ok {
		t.Fatal("unknown environment fell back")
	}
	for _, invalid := range []string{"", "[]", "null", raw + "{}", `[{"environmentId":"bad","scope":"sg-managed-cli"}]`, `[{"environmentId":"` + sg + `","scope":"*"}]`, `[{"environmentId":"` + sg + `","scope":"sg","extra":true}]`, `[{"environmentId":"` + sg + `","scope":"sg"},{"environmentId":"` + sg + `","scope":"cn"}]`} {
		if _, err := ParseScopeBindings(invalid); err == nil {
			t.Fatalf("accepted invalid bindings %q", invalid)
		}
	}
}
