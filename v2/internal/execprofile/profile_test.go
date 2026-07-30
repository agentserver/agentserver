package execprofile

import (
	"slices"
	"testing"
)

func TestPhase1ProcessProfileIsExactAndDefensivelyCopied(t *testing.T) {
	want := []string{
		"process/start",
		"process/read",
		"process/write",
		"process/terminate",
	}
	got := ProcessMethods()
	if !slices.Equal(got, want) {
		t.Fatalf("ProcessMethods() = %q, want %q", got, want)
	}
	got[0] = "process/signal"
	if !slices.Equal(ProcessMethods(), want) {
		t.Fatal("ProcessMethods exposed mutable profile storage")
	}
	for _, method := range want {
		if !AllowsProcessMethod(method) {
			t.Fatalf("AllowsProcessMethod(%q) = false", method)
		}
	}
	for _, excluded := range []string{"process/signal", "process/kill", "fs/readFile", ""} {
		if AllowsProcessMethod(excluded) {
			t.Fatalf("AllowsProcessMethod(%q) = true", excluded)
		}
	}
}

func TestPhase1NotificationsAreAgentxToGatewayOnly(t *testing.T) {
	want := []string{"process/output", "process/exited", "process/closed"}
	if got := ProcessNotifications(); !slices.Equal(got, want) {
		t.Fatalf("ProcessNotifications() = %q, want %q", got, want)
	}
	for _, method := range want {
		if !AllowsProcessNotification(method) {
			t.Fatalf("AllowsProcessNotification(%q) = false", method)
		}
	}
	if AllowsProcessNotification("initialized") {
		t.Fatal("remote lifecycle leaked into stock process notifications")
	}
}
