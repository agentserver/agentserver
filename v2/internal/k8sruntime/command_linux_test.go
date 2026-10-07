//go:build linux

package k8sruntime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func TestRuntimeProxyCannotBeOverriddenByCommand(t *testing.T) {
	c := Config{Workspace: t.TempDir(), ProxyURL: "socks5h://proxy.example:1080"}
	r := sandboxcontract.RunCommandRequest{Executable: "bkectl", WorkingDirectory: c.Workspace, WorkspaceAccess: "read"}
	args, err := sandboxArguments(c, r)
	if err != nil || !strings.Contains(strings.Join(args, "\x00"), "HTTPS_PROXY\x00"+c.ProxyURL) {
		t.Fatalf("proxy projection: %v %v", args, err)
	}
	for _, key := range []string{"HTTPS_PROXY", "http_proxy", "NO_PROXY", "all_proxy"} {
		r.Environment = map[string]string{key: "attacker.example"}
		if _, err := sandboxArguments(c, r); err == nil {
			t.Fatalf("command overrode proxy with %s", key)
		}
	}
}

func liveConfig(t *testing.T) Config {
	t.Helper()
	bwrap := os.Getenv("AGENTSERVER_K8S_RUNTIME_LIVE_BWRAP")
	if bwrap == "" {
		t.Skip("set AGENTSERVER_K8S_RUNTIME_LIVE_BWRAP for Linux containment tests")
	}
	return Config{Bwrap: bwrap, Workspace: t.TempDir()}
}

func runLive(t *testing.T, c Config, r sandboxcontract.RunCommandRequest) (string, error) {
	t.Helper()
	r.WorkingDirectory = c.Workspace
	cmd, err := sandboxCommand(c, r)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Start()
	closeExtraFiles(cmd)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err = <-finished:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-finished
		t.Fatal("live command exceeded test deadline")
	}
	return out.String(), err
}

func TestLinuxLiveWorkspaceAccessAndSecretDescriptor(t *testing.T) {
	c := liveConfig(t)
	request := sandboxcontract.RunCommandRequest{Executable: "/bin/sh", Arguments: []string{"-c", "printf %s \"$BYTECLOUD_AUTH_SECRET_ACCESS_KEY\"; printf ok > result"}, WorkingDirectory: c.Workspace, WorkspaceAccess: "write", Environment: map[string]string{"BYTECLOUD_AUTH_SECRET_ACCESS_KEY": "test-secret-only"}}
	cmd, err := sandboxCommand(c, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "test-secret-only") || strings.Contains(strings.Join(cmd.Env, " "), "test-secret-only") || len(cmd.ExtraFiles) != 1 {
		closeExtraFiles(cmd)
		t.Fatal("credential leaked to helper argv or ambient env")
	}
	raw, err := io.ReadAll(cmd.ExtraFiles[0])
	closeExtraFiles(cmd)
	if err != nil || !bytes.Contains(raw, []byte("test-secret-only")) {
		t.Fatal("credential not carried in anonymous descriptor")
	}
	clear(raw)
	out, err := runLive(t, c, request)
	if err != nil || out != "test-secret-only" {
		t.Fatalf("write/env %q %v", out, err)
	}
	if content, err := os.ReadFile(filepath.Join(c.Workspace, "result")); err != nil || string(content) != "ok" {
		t.Fatal("workspace writes not persisted")
	}
	request.WorkspaceAccess = "read"
	request.Arguments = []string{"-c", "printf forbidden > denied"}
	if out, err := runLive(t, c, request); err == nil {
		t.Fatalf("read-only command wrote workspace: %s", out)
	}
	if _, err := os.Stat(filepath.Join(c.Workspace, "denied")); !os.IsNotExist(err) {
		t.Fatal("read-only file exists")
	}
	request.Environment = map[string]string{"PATH": "/workspace"}
	if _, err := sandboxCommand(c, request); err == nil {
		t.Fatal("caller overrode trusted executable search path")
	}
}

func TestLinuxLiveNoRuntimeMaterialAndNoBackgroundSurvivor(t *testing.T) {
	c := liveConfig(t)
	outside := filepath.Join(t.TempDir(), "runtime-secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	r := sandboxcontract.RunCommandRequest{Executable: "/bin/sh", WorkspaceAccess: "write", Arguments: []string{"-c", "test ! -e /var/run/agentserver/runtime-tls/tls.key && test ! -e '" + outside + "' && test ! -e /var/run/secrets/kubernetes.io/serviceaccount/token"}}
	if out, err := runLive(t, c, r); err != nil {
		t.Fatalf("material exposed %q %v", out, err)
	}
	r.Arguments = []string{"-c", "setsid /bin/sh -c 'sleep 0.3; printf escaped > survived' >/dev/null 2>&1 & exit 0"}
	if out, err := runLive(t, c, r); err != nil {
		t.Fatalf("background test %q %v", out, err)
	}
	select {
	case <-time.After(500 * time.Millisecond):
	case <-t.Context().Done():
		t.Fatal(context.Cause(t.Context()))
	}
	if _, err := os.Stat(filepath.Join(c.Workspace, "survived")); !os.IsNotExist(err) {
		t.Fatal("detached child escaped process namespace teardown")
	}
}
