package corecredentials

import (
	"context"
	"strings"
	"testing"
)

func TestGitCredentialIsHostBoundAndProcessOnly(t *testing.T) {
	p := NewGitProvider()
	secret := []byte(`{"host":"code.byted.org","username":"git-user","token":"test-git-token"}`)
	upload, err := p.ValidateUpload("https-token", secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(upload.PublicMetadata), "test-git-token") {
		t.Fatal("Git token leaked into public metadata")
	}
	b := Binding{Kind: "git", AuthType: "https-token"}
	result, err := p.Materialize(context.Background(), b, upload.Secret, UseRequest{Host: "code.byted.org", Method: "PROCESS_ENV"})
	if err != nil || result.Environment[GitTokenEnvironment] != "test-git-token" || len(result.Headers) != 0 {
		t.Fatalf("Git materialization contract: %v", err)
	}
	for _, req := range []UseRequest{{Host: "other.example", Method: "PROCESS_ENV"}, {Host: "code.byted.org", Method: "GET"}} {
		if _, err := p.Materialize(context.Background(), b, upload.Secret, req); err == nil {
			t.Fatal("Git credential escaped host/process scope")
		}
	}
}

func TestGitCredentialRejectsMalformedSecret(t *testing.T) {
	p := NewGitProvider()
	for _, raw := range []string{
		`{"host":"code.byted.org","username":"u","token":""}`,
		`{"host":"other.example","username":"u","token":"secret"}`,
		`{"host":"code.byted.org","username":"u:x","token":"secret"}`,
		`{"host":"code.byted.org","username":"u","token":"secret\n"}`,
		`{"host":"code.byted.org","username":"u","token":"secret","extra":true}`,
	} {
		if _, err := p.ValidateUpload("https-token", []byte(raw)); err == nil {
			t.Fatal("invalid Git secret accepted")
		}
	}
}
