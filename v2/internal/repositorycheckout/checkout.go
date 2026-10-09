// Package repositorycheckout owns initial Git materialization on the executor.
// Its storage root and metadata must not be mounted into model processes; only
// Result.Tree is exposed as the session workspace. The caller supplies the
// session's exclusive persistent-volume lease for the whole operation.
package repositorycheckout

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

type Request struct {
	// CheckoutID is a Core-issued immutable session repository binding, not a
	// tool argument or a hash of mutable repository contents.
	CheckoutID string
	Source     workspacerepository.Source
}
type Credential struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}
type Result struct {
	Tree, Commit string
	Created      bool
}
type metadata struct {
	Version    int    `json:"version"`
	CheckoutID string `json:"checkoutId"`
	URL        string `json:"url"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
}
type Manager struct {
	storage, git string
	mu           sync.Mutex
	run          func(context.Context, string, []string, []string) ([]byte, error)
}

func New(storage, git string) (*Manager, error) {
	if !filepath.IsAbs(storage) || filepath.Clean(storage) != storage || storage == string(filepath.Separator) || !filepath.IsAbs(git) || filepath.Clean(git) != git {
		return nil, errors.New("repository checkout requires operator-owned absolute storage and Git paths")
	}
	info, err := os.Lstat(storage)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("repository storage is unavailable")
	}
	m := &Manager{storage: storage, git: git}
	m.run = m.runGit
	return m, nil
}

// Prepare clones only into a new private staging directory. Resume never runs
// Git, fetches, resets or cleans the existing worktree: user changes survive
// process/Pod recreation and even modifications to .git/config cannot trigger
// a credential helper. Ref changes require a different CheckoutID.
func (m *Manager) Prepare(ctx context.Context, r Request, credential *Credential) (Result, error) {
	if !identifier.MatchString(r.CheckoutID) || r.CheckoutID == "00000000-0000-0000-0000-000000000000" {
		return Result{}, errors.New("invalid checkout identity")
	}
	if err := r.Source.Validate(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	destination := filepath.Join(m.storage, r.CheckoutID)
	if _, err := os.Lstat(destination); err == nil {
		return m.resume(r, destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, errors.New("repository storage lookup failed")
	}
	if (r.Source.CredentialBindingID != "") != (credential != nil) {
		return Result{}, errors.New("repository preparation credential binding mismatch")
	}
	if credential != nil && (credential.Username == "" || len(credential.Username) > 256 || strings.ContainsAny(credential.Username, ":\x00\r\n") || credential.Token == "" || len(credential.Token) > 8192 || strings.ContainsAny(credential.Token, " \t\r\n\x00")) {
		return Result{}, errors.New("invalid repository preparation credential")
	}
	stage, err := os.MkdirTemp(m.storage, ".checkout-")
	if err != nil {
		return Result{}, errors.New("repository staging unavailable")
	}
	// Only this invocation's newly-created staging directory is removed.
	// Existing session trees are never deletion targets of Prepare.
	defer os.RemoveAll(stage)
	tree := filepath.Join(stage, "tree")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	baseEnv := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_ATTR_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1"}
	invoke := func(dir string, args, env []string) ([]byte, error) {
		out, err := m.run(ctx, dir, args, env)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("repository checkout failed; check repository URL, ref, Git access and executor connectivity")
		}
		return out, nil
	}
	if _, err := invoke(stage, []string{"init", "--quiet", "--template=", tree}, baseEnv); err != nil {
		return Result{}, err
	}
	fetchEnv := append([]string(nil), baseEnv...)
	if credential != nil {
		encoded := base64.StdEncoding.EncodeToString([]byte(credential.Username + ":" + credential.Token))
		// Git config delivered through environment is not persisted in the
		// checkout and never appears in argv. The exact HTTPS origin is fixed;
		// redirects, external protocols and helpers are disabled by runGit.
		fetchEnv = append(fetchEnv, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://"+workspacerepository.GitHost+"/.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+encoded)
	}
	ref := r.Source.Ref
	if ref == "" {
		ref = "HEAD"
	}
	_, fetchErr := invoke(tree, []string{"fetch", "--quiet", "--depth=1", "--no-tags", "--no-recurse-submodules", "--", r.Source.URL, ref}, fetchEnv)
	for i := range fetchEnv {
		fetchEnv[i] = ""
	}
	if fetchErr != nil {
		return Result{}, fetchErr
	}
	resolved, err := invoke(tree, []string{"rev-parse", "--verify", "FETCH_HEAD^{commit}"}, baseEnv)
	if err != nil {
		return Result{}, err
	}
	commit := strings.TrimSpace(string(resolved))
	if !commitID.MatchString(commit) {
		return Result{}, errors.New("repository fetch did not resolve a commit")
	}
	if _, err := invoke(tree, []string{"checkout", "--quiet", "--detach", commit}, baseEnv); err != nil {
		return Result{}, err
	}
	if _, err := invoke(tree, []string{"remote", "add", "origin", r.Source.URL}, baseEnv); err != nil {
		return Result{}, err
	}
	// Containment is validated before publication, so a missing cwd or a
	// symlink to service files cannot leave an apparently ready checkout.
	if err := checkDirectory(tree, r.Source.WorkingDirectory); err != nil {
		return Result{}, err
	}
	meta := metadata{Version: 1, CheckoutID: r.CheckoutID, URL: r.Source.URL, Ref: r.Source.Ref, Commit: commit}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"), raw, 0600); err != nil {
		return Result{}, errors.New("repository metadata write failed")
	}
	// Cross-process exclusivity comes from the caller's session/PVC lease.
	// Never overwrite a destination that appeared during preparation.
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, errors.New("repository checkout appeared during preparation")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, errors.New("repository storage lookup failed")
	}
	if err := os.Rename(stage, destination); err != nil {
		return Result{}, errors.New("repository publication failed")
	}
	return Result{Tree: filepath.Join(destination, "tree"), Commit: commit, Created: true}, nil
}

func (m *Manager) resume(r Request, dir string) (Result, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return Result{}, errors.New("repository checkout directory is invalid")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, errors.New("repository checkout unavailable")
	}
	defer root.Close()
	f, err := root.Open("metadata.json")
	if err != nil {
		return Result{}, errors.New("repository checkout metadata unavailable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return Result{}, errors.New("repository metadata exceeds bounds")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var meta metadata
	if d.Decode(&meta) != nil || meta.Version != 1 || meta.CheckoutID != r.CheckoutID || meta.URL != r.Source.URL || meta.Ref != r.Source.Ref || !commitID.MatchString(meta.Commit) {
		return Result{}, errors.New("repository identity changed; select a new checkout instead of overwriting session files")
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) {
		return Result{}, errors.New("repository checkout metadata invalid")
	}
	tree := filepath.Join(dir, "tree")
	if err := checkDirectory(tree, r.Source.WorkingDirectory); err != nil {
		return Result{}, err
	}
	return Result{Tree: tree, Commit: meta.Commit}, nil
}

func checkDirectory(tree, cwd string) error {
	info, err := os.Lstat(tree)
	if err != nil || !info.IsDir() {
		return errors.New("repository tree is unavailable")
	}
	root, err := os.OpenRoot(tree)
	if err != nil {
		return errors.New("repository tree is unavailable")
	}
	defer root.Close()
	if cwd == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(cwd, "/") {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() {
			return errors.New("repository working directory is missing or contains a symlink")
		}
	}
	return nil
}

func (m *Manager) runGit(ctx context.Context, dir string, args, env []string) ([]byte, error) {
	config := []string{"-c", "credential.helper=", "-c", "core.hooksPath=/dev/null", "-c", "http.followRedirects=false", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "submodule.recurse=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	cmd := exec.CommandContext(ctx, m.git, append(config, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	var out boundedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4096 {
		return 0, errors.New("Git response exceeds bound")
	}
	return b.Buffer.Write(p)
}
