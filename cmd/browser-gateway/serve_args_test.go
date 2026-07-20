package main

import "testing"

func TestParseServeArgs_Default(t *testing.T) {
	a, err := parseServeArgs(nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.ListenAddr != ":8088" {
		t.Errorf("ListenAddr = %q, want :8088", a.ListenAddr)
	}
}

func TestParseServeArgs_Flag(t *testing.T) {
	a, err := parseServeArgs([]string{"--listen-addr", ":9999"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", a.ListenAddr)
	}
}
