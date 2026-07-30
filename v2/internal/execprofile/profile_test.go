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

func TestFilesystemReadProfileIsAdditiveAndExact(t *testing.T) {
	if !AllowsEnvironmentProfile(Version) || SupportsFilesystemRead(Version) {
		t.Fatal("process-only profile capability classification is wrong")
	}
	if !AllowsEnvironmentProfile(FilesystemReadVersion) || !SupportsFilesystemRead(FilesystemReadVersion) {
		t.Fatal("filesystem-read profile capability classification is wrong")
	}
	if AllowsEnvironmentProfile("filesystem-read-v1") || AllowsEnvironmentProfile("") {
		t.Fatal("partial or empty environment profile was accepted")
	}
	want := []string{"agentx/fs/readFileBlock"}
	got := FilesystemReadMethods()
	if !slices.Equal(got, want) || !AllowsFilesystemReadMethod(want[0]) {
		t.Fatalf("filesystem read methods = %q, want %q", got, want)
	}
	got[0] = "fs/readFile"
	if !slices.Equal(FilesystemReadMethods(), want) || AllowsFilesystemReadMethod("fs/readFile") {
		t.Fatal("filesystem read profile is mutable or admits unbounded stock read")
	}
}
