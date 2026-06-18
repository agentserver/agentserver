package noise

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIdentityGenerate_RoundTripsPublicKey(t *testing.T) {
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pk := id.PublicKey()
	if pk.Suite != SuiteName {
		t.Errorf("suite = %q, want %q", pk.Suite, SuiteName)
	}
	dhPub, kemPub, err := pk.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dhPub.Bytes()) != X25519PubLen {
		t.Errorf("dh pub len = %d, want %d", len(dhPub.Bytes()), X25519PubLen)
	}
	if len(kemPub.Bytes()) != MLKEM768PubLen {
		t.Errorf("kem pub len = %d, want %d", len(kemPub.Bytes()), MLKEM768PubLen)
	}
}

func TestPublicKey_RejectsUnknownSuite(t *testing.T) {
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pk := id.PublicKey()
	pk.Suite = "Noise_Nonsense"
	if _, _, err := pk.Decode(); err == nil || !strings.Contains(err.Error(), "unsupported suite") {
		t.Errorf("decode wrong-suite err = %v, want unsupported suite", err)
	}
}

func TestPublicKey_JSONShape(t *testing.T) {
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	raw, err := json.Marshal(id.PublicKey())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, want := range []string{"suite", "x25519_public_key", "mlkem768_public_key"} {
		if _, ok := probe[want]; !ok {
			t.Errorf("missing field %q in %s", want, raw)
		}
	}
}
