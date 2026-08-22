package productiondeploy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExampleConfigOmitsManagedSummaryLocks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range [][]byte{
		[]byte(`"profileId"`), []byte(`"bindingSha256"`),
		[]byte(`"runtimeProfileSha256"`), []byte(`"packSetSha256"`),
	} {
		if bytes.Contains(raw, field) {
			t.Fatalf("example config still contains removed managed summary field %s", field)
		}
	}
	if _, err := ParseConfig(raw); err != nil {
		t.Fatal(err)
	}
}
