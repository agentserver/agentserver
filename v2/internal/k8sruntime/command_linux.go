//go:build linux

package k8sruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"golang.org/x/sys/unix"
)

func sandboxCommand(config Config, r sandboxcontract.RunCommandRequest) (*exec.Cmd, error) {
	args, err := sandboxArguments(config, r)
	if err != nil {
		return nil, err
	}
	fd, err := unix.MemfdCreate("agentserver-process-args", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, errors.New("runtime argument descriptor unavailable")
	}
	file := os.NewFile(uintptr(fd), "runtime-args")
	raw := []byte(strings.Join(args, "\x00") + "\x00")
	defer clear(raw)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return nil, errors.New("runtime argument descriptor write failed")
	}
	if _, err = file.Seek(0, 0); err != nil {
		file.Close()
		return nil, err
	}
	if _, err = unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		file.Close()
		return nil, err
	}
	commandArgs := append([]string{"--args", "3", "--", r.Executable}, r.Arguments...)
	cmd := exec.Command(config.Bwrap, commandArgs...)
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd, nil
}

func sandboxArguments(c Config, r sandboxcontract.RunCommandRequest) ([]string, error) {
	rel, err := filepath.Rel(c.Workspace, r.WorkingDirectory)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, errors.New("cwd outside workspace")
	}
	// Resolve the cwd through Root now; bubblewrap remounts that same workspace
	// root and makes all other paths immutable/unreachable in the child.
	root, err := os.OpenRoot(c.Workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(rel)
	if err != nil {
		return nil, errors.New("cwd unavailable")
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		return nil, errors.New("cwd must be directory")
	}
	if r.WorkspaceAccess != "" && r.WorkspaceAccess != "read" && r.WorkspaceAccess != "write" {
		return nil, errors.New("invalid workspace access")
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv"}
	for _, dir := range []string{"/usr", "/bin", "/lib"} {
		args = append(args, "--ro-bind", dir, dir)
	}
	args = append(args, "--ro-bind-try", "/lib64", "/lib64", "--dir", "/etc")
	for _, file := range []string{"/etc/ssl", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group"} {
		args = append(args, "--ro-bind-try", file, file)
	}
	// No host procfs is exposed to commands. Mounting a nested procfs under a
	// container's masked proc mount is rejected on ordinary runtimes; an empty
	// /proc is a stricter, explicit CLI profile (not a shared host proc fallback).
	args = append(args, "--ro-bind", "/opt/agentserver/packs", "/opt/agentserver/packs", "--dir", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/tmp/home")
	bind := "--bind"
	if r.WorkspaceAccess == "read" {
		bind = "--ro-bind"
	}
	args = append(args, bind, c.Workspace, c.Workspace, "--chdir", r.WorkingDirectory,
		"--setenv", "PATH", "/usr/local/bin:/usr/bin:/bin", "--setenv", "HOME", "/tmp/home", "--setenv", "TMPDIR", "/tmp", "--setenv", "LANG", "C.UTF-8")
	// ByteCloud/bkectl needs the internal egress route. Lark's public CDN must
	// remain direct: routing open.feishu.cn through that internal SOCKS tunnel
	// was observed to time out in the SG canary.
	if c.ProxyURL != "" && (r.Executable == "bkectl" || r.Executable == "/usr/local/bin/bkectl") {
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
			args = append(args, "--setenv", key, c.ProxyURL)
		}
	}
	keys := make([]string, 0, len(r.Environment))
	for k := range r.Environment {
		if k == "PATH" && r.Environment[k] == "/usr/local/bin:/usr/bin:/bin" {
			continue
		}
		if k == "PATH" || k == "HOME" || k == "TMPDIR" || k == "ENV" || k == "BASH_ENV" || strings.HasPrefix(k, "LD_") || (c.ProxyURL != "" && strings.HasSuffix(strings.ToUpper(k), "_PROXY")) {
			return nil, errors.New("reserved runtime environment")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// sandboxCommand passes the complete null-delimited argument vector via a
	// sealed anonymous descriptor. These values never appear in helper argv.
	for _, key := range keys {
		args = append(args, "--setenv", key, r.Environment[key])
	}
	return args, nil
}
