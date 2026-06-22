# cc-app-gateway Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add S3 workspace persistence + session resume to cc-app-gateway so the same `(workspaceID, sessionID)` tuple has memory across calls — unblocks Phase 4's IM intake.

**Architecture:** Per-turn `claude --print` subprocess still the model. workspace.Setup downloads prior turn's claude-home tarball from S3 (or no-ops on miss); workspace.Teardown backgrounds tar+gz+S3 Put + tmpdir cleanup. runner picks `--resume` vs `--session-id` from a new SessionMode field. Per-session sync.Map mutex serializes turns within a pod (cross-pod race deferred). `Server.Shutdown` waits for pending Teardown goroutines via sync.WaitGroup.

**Tech Stack:** Go 1.26, stdlib `archive/tar` + `compress/gzip`, `github.com/aws/aws-sdk-go-v2/{aws,config,service/s3,service/s3/types}` (already in go.mod from codex). Default credential chain (NOT codex's static-creds pattern) for IRSA support. minio for integration testing.

**Spec:** `/root/agentserver/.claude/worktrees/cc-app-gateway-phase2/docs/superpowers/specs/2026-06-21-cc-app-gateway-phase2-design.md` (read § Architecture, § Concurrency, § Audit revisions before starting).

**Phase 0 artifacts (still applicable):** `/tmp/cc-probe/probe.go` + `FINDINGS.md`. Frame schema unchanged from Phase 1.

**Working directory:** `/root/agentserver/.claude/worktrees/cc-app-gateway-phase2` (worktree on branch `feat/cc-app-gateway-phase2`, stacked on `feat/cc-app-gateway-phase1`).

**Module path:** `github.com/agentserver/agentserver`.

## Global Constraints

- Go 1.26, stdlib + already-present aws-sdk-go-v2/* deps only — no new direct deps.
- Module path `github.com/agentserver/agentserver`.
- `claude --print` 2.1.185 (pinned in Dockerfile.cc-app-gateway from Phase 1) is the subprocess.
- TDD: each step's tests must fail before implementation, pass after. RED + GREEN evidence in report.
- ProjectDir layout MUST be `<tmpRoot>/<workspaceID>/<sessionID>/project` — stable across turns for the same (workspace, session) tuple. NEVER change once shipped — claude's cwd-sanitization is load-bearing for resume. Per Open Risk #3 of spec.
- Tar root = `ClaudeDir` (NOT `ClaudeDir/projects`). Captures `.claude.json` (config), `projects/<sanitized-cwd>/` (jsonl + memory subtree), `sessions/` (empty dir). Per spec §Component changes/workspace.Teardown.
- Teardown MUST `os.RemoveAll(<ClaudeDir>/backups)` BEFORE tarring — claude writes 1 backup file per spawn; pruning prevents tarball file-count explosion. Per spec Audit Revision #2.
- AWS credentials via `config.LoadDefaultConfig(ctx)` (default chain — env vars / IRSA / shared config), NOT codex's `credentials.NewStaticCredentialsProvider` pattern. IRSA support is required for prod EKS. Per spec Audit Revision #5.
- S3 readyz probe key: `cc-app-gateway/__readyz__/probe` (inside the prefix the gateway has IAM perms on). Per spec Audit Revision #4.
- S3 key for session tarballs: `cc-app-gateway/<workspaceID>/<sessionID>.tar.gz`.
- New env vars: `CCAPPGW_S3_ENDPOINT` (optional, MinIO), `CCAPPGW_S3_REGION` (required when enabled=true), `CCAPPGW_S3_BUCKET` (required), `CCAPPGW_S3_PATH_STYLE` (bool, optional). AWS creds via SDK default chain.
- Per-session mutex MUST be acquired in TurnHandler.ServeHTTP before Setup and released by the Teardown goroutine — within-pod sequential turns block, never corrupt state. Per spec §Concurrency.

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/ccappgateway/workspace/s3.go` | NEW | `ObjectStore` interface; `TarUpload(ctx, store, key, src)`; `TarDownload(ctx, store, key, dst)`; `ErrObjectNotFound` sentinel. Mirror codex `codexhome/s3.go` lines 44-151 verbatim (only package decl + function-vs-method shape differs). |
| `internal/ccappgateway/workspace/s3_test.go` | NEW | Tar round-trip via map-backed fake ObjectStore; path-traversal safety (`..` rejection); empty dir survival. |
| `internal/ccappgateway/workspace/workspace.go` | MODIFY | Extend `Workspace` struct (add `IsResume`, private `workspaceID`/`sessionID`); change `Setup` signature to take `workspaceID, sessionID, store ObjectStore` and route to per-session subdir; change `Teardown` to `Teardown(ctx, store) error` and add backups-prune + tar+upload before RemoveAll. |
| `internal/ccappgateway/workspace/workspace_test.go` | MODIFY | New tests: Setup hit path (untars), Setup miss path (`IsResume=false`), Teardown round-trip, per-session-subdir naming stable, backups/ pruned before tarring. Existing tests adapted for new signatures. |
| `internal/ccappgateway/s3client.go` | NEW | `S3Config` struct; `NewS3Client(ctx, cfg) (workspace.ObjectStore, error)` using `config.LoadDefaultConfig`. `Put`/`Get`/`Delete` translating `*types.NoSuchKey` → `workspace.ErrObjectNotFound`. |
| `internal/ccappgateway/s3client_test.go` | NEW | Mock-server-based: NewS3Client returns no err with valid cfg; missing region returns clear error. Real S3 round-trip is covered by integration test. |
| `internal/ccappgateway/config.go` | MODIFY | Add `S3Endpoint`, `S3Region`, `S3Bucket`, `S3PathStyle` fields. Add env-var loading. Required (when enabled): `S3Region`, `S3Bucket`. |
| `internal/ccappgateway/config_test.go` | MODIFY | New cases for S3 env var loading + required-var validation. |
| `internal/ccappgateway/runner/options.go` | MODIFY | Add `SessionMode string` to `RunInput`. Update `BuildArgs` to switch on it (`fresh` → `--session-id`, `resume` → `--resume`). |
| `internal/ccappgateway/runner/options_test.go` | MODIFY | New cases: BuildArgs with SessionMode="fresh", "resume", default(""). |
| `internal/ccappgateway/server.go` | MODIFY | Add `Store workspace.ObjectStore`, `sessionLocks sync.Map`, `teardownWG sync.WaitGroup` fields. NewServer constructs S3 client. Routes get a third readyz check (S3 probe). Shutdown waits for teardownWG. |
| `internal/ccappgateway/server_test.go` | MODIFY | New cases: readyz returns 503 on S3 unreachable; Shutdown drains pending Teardowns. |
| `internal/ccappgateway/turn_api.go` | MODIFY | `TurnHandler` gets `Store` + reference to server's sessionLocks/teardownWG. ServeHTTP acquires per-session mutex, calls workspace.Setup with new signature, picks SessionMode from `ws.IsResume`, defers Teardown in a goroutine that releases mutex AFTER S3 Put completes. |
| `internal/ccappgateway/turn_api_test.go` | MODIFY | New cases: same-session twice → second waits for first (mutex test); ws.IsResume true → BuildArgs uses --resume; S3 Get failure (not NotFound) → 502 with code "workspace_setup_failed". |
| `deploy/helm/agentserver/templates/cc-app-gateway.yaml` | MODIFY | Add `CCAPPGW_S3_*` env vars + `envFrom: existingSecret` for AWS credentials. |
| `deploy/helm/agentserver/values.yaml` | MODIFY | Add `ccAppGateway.s3.{endpoint,region,bucket,pathStyle,existingSecret}` block. `helm template` renders error via `{{ required }}` if `ccAppGateway.enabled=true` and `s3.bucket`/`s3.region` missing. |
| `internal/ccappgateway/testdata/integration/docker-compose.yml` | MODIFY | Add minio + minio-init sidecars. Add `CCAPPGW_S3_*` + `AWS_*` env vars to cc-app-gateway service. |
| `cmd/cc-app-gateway-test-tools/main.go` | MODIFY | fake-llmproxy adds `--log-requests-to <path>` mode dumping every inbound request body — used by integration resume test. |
| `internal/ccappgateway/integration_test.go` | MODIFY | New test `TestIntegration_ResumeAcrossTurns`: turn 1 sets context, turn 2 same sessionID; assert fake-llmproxy's request log shows turn 2's request body includes turn 1's user message. |

Total: 7 new + 11 modified files. Estimated LOC: ~1800 (~900 production + ~900 tests).

---

## Task 1: ObjectStore interface + tar/untar primitives (workspace/s3.go)

**Files:**
- Create: `internal/ccappgateway/workspace/s3.go`
- Create: `internal/ccappgateway/workspace/s3_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (this is the leaf).
- Produces (later tasks use these):
  - `type ObjectStore interface { Put(ctx, key, data) error; Get(ctx, key) ([]byte, error); Delete(ctx, key) error }`
  - `var ErrObjectNotFound = errors.New("workspace: object not found")` — Get returns this on missing key.
  - `func TarUpload(ctx context.Context, store ObjectStore, key, src string) error` — walks `src`, writes tar.gz to `store` at `key`.
  - `func TarDownload(ctx context.Context, store ObjectStore, key, dst string) error` — reads `key` from `store`, untars into `dst`. Returns `ErrObjectNotFound` if absent.

The tar+untar implementation MUST mirror `internal/codexappgateway/codexhome/s3.go` lines 44-151 in behavior: only TypeReg + TypeDir entries written/read, symlinks/fifo/devices skipped, `strings.Contains(hdr.Name, "..")` path-safety rejection, file mode masked to 0o600 on untar, dir mode masked to 0o700.

- [ ] **Step 1: Write the failing test for tar round-trip**

Create `internal/ccappgateway/workspace/s3_test.go`:

```go
package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// fakeStore is a map-backed in-memory ObjectStore for tests.
type fakeStore struct{ data map[string][]byte }

func newFakeStore() *fakeStore { return &fakeStore{data: make(map[string][]byte)} }

func (f *fakeStore) Put(_ context.Context, key string, data []byte) error {
	f.data[key] = append([]byte(nil), data...)
	return nil
}

func (f *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := f.data[key]
	if !ok {
		return nil, workspace.ErrObjectNotFound
	}
	return append([]byte(nil), v...), nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

func TestTarRoundTrip(t *testing.T) {
	src := t.TempDir()
	// Populate src with: file at root, file in subdir, empty subdir.
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	store := newFakeStore()
	ctx := context.Background()
	if err := workspace.TarUpload(ctx, store, "test/key.tar.gz", src); err != nil {
		t.Fatalf("TarUpload: %v", err)
	}

	dst := t.TempDir()
	if err := workspace.TarDownload(ctx, store, "test/key.tar.gz", dst); err != nil {
		t.Fatalf("TarDownload: %v", err)
	}

	// Verify root.txt round-tripped.
	if data, err := os.ReadFile(filepath.Join(dst, "root.txt")); err != nil {
		t.Fatalf("read root.txt: %v", err)
	} else if string(data) != "root-content" {
		t.Errorf("root.txt content: got %q, want %q", data, "root-content")
	}

	// Verify nested file.
	if data, err := os.ReadFile(filepath.Join(dst, "sub", "nested.txt")); err != nil {
		t.Fatalf("read sub/nested.txt: %v", err)
	} else if string(data) != "nested-content" {
		t.Errorf("sub/nested.txt content: got %q, want %q", data, "nested-content")
	}

	// Verify empty dir survived (regression for codex's WalkDir behavior).
	info, err := os.Stat(filepath.Join(dst, "empty"))
	if err != nil {
		t.Errorf("empty dir not preserved: %v", err)
	} else if !info.IsDir() {
		t.Errorf("empty/ should be a directory")
	}
}

func TestTarDownloadObjectNotFound(t *testing.T) {
	store := newFakeStore()
	dst := t.TempDir()
	err := workspace.TarDownload(context.Background(), store, "nonexistent", dst)
	if !errors.Is(err, workspace.ErrObjectNotFound) {
		t.Errorf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestTarPathTraversalRejected(t *testing.T) {
	// Craft a tar with a path containing ".." and verify TarDownload rejects it.
	// Use raw tar+gzip writers since we control the malicious archive directly.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o600, Size: 5, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("evil!"))
	tw.Close()
	gz.Close()

	store := newFakeStore()
	store.data["evil-key"] = buf.Bytes()
	err := workspace.TarDownload(context.Background(), store, "evil-key", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "untrusted path") {
		t.Errorf("expected 'untrusted path' error, got %v", err)
	}
}
```

(imports needed: `archive/tar`, `bytes`, `compress/gzip`, `errors`, `strings` — add to the file's import block when implementing.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/ccappgateway/workspace/...
```
Expected: build error (TarUpload/TarDownload/ObjectStore/ErrObjectNotFound undefined).

- [ ] **Step 3: Implement workspace/s3.go**

```go
package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrObjectNotFound is returned by ObjectStore.Get when a key is absent.
// Implementations (s3client.go, test fakes) MUST translate their backend's
// "missing key" error to this sentinel.
var ErrObjectNotFound = errors.New("workspace: object not found")

// ObjectStore is the seam between workspace and the S3 client. Production
// uses internal/ccappgateway/s3client.go's *S3Client; tests use a
// map-backed fakeStore.
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// TarUpload tars+gzips the directory tree at src and writes it to store at key.
// Walks src with filepath.WalkDir; writes a tar header for every entry
// (including empty dirs); only files (TypeReg) carry data. Symlinks/fifo/
// devices are NOT written — claude-home contains none.
func TarUpload(ctx context.Context, store ObjectStore, key, src string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Only regular files get content; codex's pattern skips
		// symlinks/fifo/devices silently — claude-home has none.
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		_ = f.Close()
		return copyErr
	})
	if err != nil {
		return fmt.Errorf("tar walk: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gz close: %w", err)
	}
	return store.Put(ctx, key, buf.Bytes())
}

// TarDownload fetches key from store and untars into dst (which must exist
// and be owned by the caller). Returns ErrObjectNotFound if the key is absent.
// Rejects archives containing ".." path components for safety.
func TarDownload(ctx context.Context, store ObjectStore, key, dst string) error {
	data, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("untrusted path: %s", hdr.Name)
		}
		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := fs.FileMode(hdr.Mode) & 0o700
			if mode == 0 {
				mode = 0o700
			}
			if err := os.MkdirAll(target, mode); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			mode := fs.FileMode(hdr.Mode) & 0o600
			if mode == 0 {
				mode = 0o600
			}
			f, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("open %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("copy %s: %w", target, err)
			}
			_ = f.Close()
		default:
			// Skip symlinks / fifo / devices — claude doesn't write them.
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/workspace/...
```
Expected: 3 new tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/workspace/s3.go internal/ccappgateway/workspace/s3_test.go
git commit -m "feat(cc-app-gateway): workspace tar+gzip via ObjectStore seam"
```

---

## Task 2: Extend Workspace struct + Setup/Teardown signatures

**Files:**
- Modify: `internal/ccappgateway/workspace/workspace.go`
- Modify: `internal/ccappgateway/workspace/workspace_test.go`

**Interfaces:**
- Consumes (from Task 1): `ObjectStore` interface, `ErrObjectNotFound`, `TarUpload`, `TarDownload`.
- Produces (later tasks use these):
  - `type Workspace struct { TempDir, ClaudeDir, ProjectDir string; IsResume bool; /* private fields */ }`
  - `func Setup(ctx context.Context, tmpRoot, workspaceID, sessionID string, store ObjectStore) (*Workspace, error)` — IsResume=true on hit, false on miss; non-NotFound S3 errors are returned (caller decides whether to graceful-degrade).
  - `func (w *Workspace) Teardown(ctx context.Context, store ObjectStore) error` — prunes backups/, tars+uploads ClaudeDir, removes TempDir. Idempotent (second call no-op).
  - The S3 key for a session: `cc-app-gateway/<workspaceID>/<sessionID>.tar.gz` — exposed via private `(*Workspace) s3Key() string` helper (used internally by Teardown).

ProjectDir layout: `<tmpRoot>/<workspaceID>/<sessionID>/project`. TempDir is `<tmpRoot>/<workspaceID>/<sessionID>/` (the per-session subtree). This is the load-bearing layout — locked forever per spec Open Risk #3.

- [ ] **Step 1: Write failing tests for the new signatures**

Replace `internal/ccappgateway/workspace/workspace_test.go` Phase 1 tests with this Phase 2 version (existing tests had different signatures and would no longer compile):

```go
package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

func TestSetupCreatesPerSessionSubdirs(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	wantTemp := filepath.Join(tmpRoot, "ws_abc", "sid_123")
	if ws.TempDir != wantTemp {
		t.Errorf("TempDir: got %q, want %q", ws.TempDir, wantTemp)
	}
	if ws.ClaudeDir != filepath.Join(wantTemp, "claude-home") {
		t.Errorf("ClaudeDir wrong: %q", ws.ClaudeDir)
	}
	if ws.ProjectDir != filepath.Join(wantTemp, "project") {
		t.Errorf("ProjectDir wrong: %q", ws.ProjectDir)
	}
	if ws.IsResume {
		t.Error("IsResume should be false on first Setup (empty store)")
	}
	// Confirm directories actually exist.
	for _, d := range []string{ws.ClaudeDir, ws.ProjectDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("dir %q missing: %v", d, err)
		} else if !info.IsDir() {
			t.Errorf("%q is not a dir", d)
		} else if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%q perm: got %#o, want 0700", d, perm)
		}
	}
}

func TestSetupHitDownloadsTarball(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	// Seed store with a tarball containing one file under projects/.
	// Use TarUpload from Task 1 to create the seed naturally.
	seedDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(seedDir, "projects", "-tmp-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "projects", "-tmp-x", "sid_123.jsonl"), []byte("seeded-jsonl-row\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "cc-app-gateway/ws_abc/sid_123.tar.gz"
	if err := workspace.TarUpload(context.Background(), store, key, seedDir); err != nil {
		t.Fatalf("seed TarUpload: %v", err)
	}

	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !ws.IsResume {
		t.Error("IsResume should be true on Setup hit")
	}
	// Confirm seeded file was untarred into ClaudeDir.
	got, err := os.ReadFile(filepath.Join(ws.ClaudeDir, "projects", "-tmp-x", "sid_123.jsonl"))
	if err != nil {
		t.Fatalf("seeded jsonl missing: %v", err)
	}
	if string(got) != "seeded-jsonl-row\n" {
		t.Errorf("seeded content lost: %q", got)
	}
}

func TestSetupNonNotFoundErrorReturned(t *testing.T) {
	// Use an ObjectStore that returns a non-NotFound error on Get.
	errStore := &errorStore{err: errors.New("network unreachable")}
	_, err := workspace.Setup(context.Background(), t.TempDir(), "ws", "sid", errStore)
	if err == nil {
		t.Fatal("Setup should return error on non-NotFound S3 failure")
	}
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Errorf("error should wrap S3 err, got: %v", err)
	}
}

func TestTeardownPrunesBackupsBeforeUpload(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate claude writing a backup file.
	backupDir := filepath.Join(ws.ClaudeDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, ".claude.json.backup.123"), []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also put a real file in projects/ so the tarball has something.
	pdir := filepath.Join(ws.ClaudeDir, "projects", "-tmp-x")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "sid_123.jsonl"), []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// Untar the stored tarball into a verify dir, assert no backups/ entry.
	verifyDir := t.TempDir()
	key := "cc-app-gateway/ws_abc/sid_123.tar.gz"
	if err := workspace.TarDownload(context.Background(), store, key, verifyDir); err != nil {
		t.Fatalf("verify TarDownload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "backups")); !os.IsNotExist(err) {
		t.Errorf("backups/ should be absent from tarball, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "projects", "-tmp-x", "sid_123.jsonl")); err != nil {
		t.Errorf("projects/ content lost: %v", err)
	}
	// Confirm TempDir was removed.
	if _, err := os.Stat(ws.TempDir); !os.IsNotExist(err) {
		t.Errorf("TempDir should be removed, stat err=%v", err)
	}
}

func TestTeardownIdempotent(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	// Second Teardown should not error.
	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Errorf("second Teardown should be no-op, got: %v", err)
	}
}

// errorStore is an ObjectStore whose Get always fails with the wrapped error.
type errorStore struct{ err error }

func (e *errorStore) Get(_ context.Context, _ string) ([]byte, error) { return nil, e.err }
func (e *errorStore) Put(_ context.Context, _ string, _ []byte) error { return e.err }
func (e *errorStore) Delete(_ context.Context, _ string) error        { return e.err }
```

The Phase 1 `TestSetupAutoCreatesTmpRoot`, `TestTeardownRemovesTempDir`, etc., are DROPPED — their signatures don't exist anymore. The new test set covers the same behaviors plus the new ones.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/ccappgateway/workspace/...
```
Expected: build error (Setup new signature undefined, Teardown new signature undefined).

- [ ] **Step 3: Rewrite workspace.go**

```go
// Package workspace manages ephemeral per-turn local directories for claude execution.
// Phase 2 adds S3 persistence: Setup downloads prior turn's claude-home tarball,
// Teardown tars + uploads + cleans up.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace is an ephemeral per-turn local view of a session's state.
// The TempDir/ClaudeDir/ProjectDir layout is keyed on (workspaceID, sessionID)
// so the layout is STABLE across turns — load-bearing for claude's --resume
// to find its prior jsonl (claude's cwd sanitization makes the project
// subdir name a function of cmd.Dir; if cmd.Dir varied per turn, --resume
// would silently fail).
type Workspace struct {
	TempDir    string // <tmpRoot>/<workspaceID>/<sessionID>/
	ClaudeDir  string // <tmpRoot>/<workspaceID>/<sessionID>/claude-home/
	ProjectDir string // <tmpRoot>/<workspaceID>/<sessionID>/project/
	IsResume   bool   // true if S3 had a tarball for this session — runner uses --resume

	workspaceID string // for s3Key()
	sessionID   string // for s3Key()
}

// Setup creates a fresh tmpdir under tmpRoot at <tmpRoot>/<workspaceID>/<sessionID>/
// with claude-home + project subdirs (0700 perms). Then tries to download a
// prior tarball from store at the session's key:
//   - Hit (no error):                IsResume = true, ClaudeDir populated
//   - Miss (ErrObjectNotFound):      IsResume = false, ClaudeDir empty
//   - Other error (network, etc.):   returns the error wrapped — caller decides
//                                    whether to fail the turn or graceful-degrade
//
// Caller MUST call Teardown when done (preferably in a deferred goroutine
// per spec § Concurrency).
func Setup(ctx context.Context, tmpRoot, workspaceID, sessionID string, store ObjectStore) (*Workspace, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace.Setup: workspaceID required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("workspace.Setup: sessionID required")
	}
	// Create tmpRoot if missing (0755) — parent of per-session subtree.
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir tmpRoot: %w", err)
	}

	tempDir := filepath.Join(tmpRoot, workspaceID, sessionID)
	claudeDir := filepath.Join(tempDir, "claude-home")
	projectDir := filepath.Join(tempDir, "project")
	for _, d := range []string{claudeDir, projectDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			// Best-effort cleanup of partial state.
			_ = os.RemoveAll(tempDir)
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	ws := &Workspace{
		TempDir:     tempDir,
		ClaudeDir:   claudeDir,
		ProjectDir:  projectDir,
		workspaceID: workspaceID,
		sessionID:   sessionID,
	}

	// Try to download a prior tarball.
	if err := TarDownload(ctx, store, ws.s3Key(), claudeDir); err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			ws.IsResume = false
			return ws, nil
		}
		// Non-NotFound: cleanup tmpdir and surface error.
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("download prior tarball: %w", err)
	}
	ws.IsResume = true
	return ws, nil
}

// Teardown prunes <ClaudeDir>/backups/ (claude writes a backup per spawn that
// would otherwise grow the tarball file count linearly), tars + gzips
// ClaudeDir, uploads to store at the session's key (OVERWRITES previous
// tarball — last-write-wins by design; cross-pod race deferred to Phase 5+),
// then removes the TempDir.
//
// Idempotent: a second call after TempDir is gone returns nil.
//
// Error precedence: if any step fails, the TempDir is STILL removed
// (fail-open) and the first error is returned. Caller logs and continues
// serving — losing one turn's history is better than failing every subsequent
// turn because of stale tmpdir.
func (w *Workspace) Teardown(ctx context.Context, store ObjectStore) error {
	if w == nil || w.TempDir == "" {
		return nil
	}
	// Idempotent: if TempDir is gone, nothing to do.
	if _, err := os.Stat(w.TempDir); os.IsNotExist(err) {
		return nil
	}

	var firstErr error

	// 1. Prune backups/ before tarring (claude's per-spawn config snapshots).
	//    claude re-creates backups/ on next spawn; it's not part of resume state.
	backupsDir := filepath.Join(w.ClaudeDir, "backups")
	if err := os.RemoveAll(backupsDir); err != nil {
		firstErr = fmt.Errorf("prune backups/: %w", err)
		// continue — pruning failure doesn't block tarring
	}

	// 2. Tar + gzip + upload (root = ClaudeDir to capture .claude.json + projects/ + sessions/).
	if err := TarUpload(ctx, store, w.s3Key(), w.ClaudeDir); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("tar+upload: %w", err)
		}
	}

	// 3. Remove TempDir regardless of upload outcome (fail-open).
	if err := os.RemoveAll(w.TempDir); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("remove TempDir: %w", err)
		}
	}

	return firstErr
}

// s3Key returns the S3 object key for this workspace's session tarball.
// Format: cc-app-gateway/<workspaceID>/<sessionID>.tar.gz — pinned forever
// (changing breaks resume for existing sessions).
func (w *Workspace) s3Key() string {
	return fmt.Sprintf("cc-app-gateway/%s/%s.tar.gz", w.workspaceID, w.sessionID)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/workspace/...
```
Expected: all new tests pass (TestSetupCreatesPerSessionSubdirs, TestSetupHitDownloadsTarball, TestSetupNonNotFoundErrorReturned, TestTeardownPrunesBackupsBeforeUpload, TestTeardownIdempotent, plus Task 1's 3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/workspace/workspace.go internal/ccappgateway/workspace/workspace_test.go
git commit -m "feat(cc-app-gateway): workspace per-session S3 persistence + IsResume"
```

---

## Task 3: S3 client wiring with default credential chain

**Files:**
- Create: `internal/ccappgateway/s3client.go`
- Create: `internal/ccappgateway/s3client_test.go`

**Interfaces:**
- Consumes (from Task 1): `workspace.ObjectStore`, `workspace.ErrObjectNotFound`.
- Produces (later tasks):
  - `type S3Config struct { Endpoint, Region, Bucket string; PathStyle bool }`
  - `func NewS3Client(ctx context.Context, cfg S3Config) (workspace.ObjectStore, error)` — uses `config.LoadDefaultConfig` (IRSA-friendly).

- [ ] **Step 1: Write failing tests**

Create `internal/ccappgateway/s3client_test.go`:

```go
package ccappgateway_test

import (
	"context"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway"
)

func TestNewS3Client_RegionRequired(t *testing.T) {
	_, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Bucket: "test-bucket",
	})
	if err == nil {
		t.Fatal("NewS3Client should fail without Region")
	}
}

func TestNewS3Client_BucketRequired(t *testing.T) {
	_, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Region: "us-east-1",
	})
	if err == nil {
		t.Fatal("NewS3Client should fail without Bucket")
	}
}

func TestNewS3Client_ValidConfig(t *testing.T) {
	// Set dummy AWS env so config.LoadDefaultConfig doesn't error reaching for IRSA.
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	store, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Region: "us-east-1",
		Bucket: "test-bucket",
	})
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	if store == nil {
		t.Fatal("NewS3Client returned nil store")
	}
}
```

Real S3 Get/Put round-trips are covered by Task 9's integration test (against minio); this unit-test file only covers config validation.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/ccappgateway/... -run TestNewS3Client
```
Expected: build error (S3Config, NewS3Client undefined).

- [ ] **Step 3: Implement s3client.go**

```go
package ccappgateway

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// S3Config bundles the S3 connection settings. AWS credentials are sourced
// from the SDK default chain (env vars, IRSA tokens, shared config, EC2/ECS
// instance metadata) — NOT explicit static creds. This is required for prod
// EKS deployments where IRSA tokens rotate automatically.
type S3Config struct {
	Endpoint  string // optional: MinIO/dev endpoint URL
	Region    string // required
	Bucket    string // required
	PathStyle bool   // true for MinIO; false for real AWS
}

// NewS3Client constructs a workspace.ObjectStore backed by aws-sdk-go-v2.
// Validates Region and Bucket are non-empty. Returns wrapped errors on
// AWS config load failure (e.g., missing credentials in dev where the
// default chain has nothing to find).
func NewS3Client(ctx context.Context, cfg S3Config) (workspace.ObjectStore, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3client: Region required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3client: Bucket required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("s3client: load aws config: %w", err)
	}

	opts := []func(*s3.Options){}
	if cfg.Endpoint != "" {
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}
	if cfg.PathStyle {
		opts = append(opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, opts...)
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

type s3Store struct {
	client *s3.Client
	bucket string
}

func (s *s3Store) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytesReader(data),
	})
	return err
}

func (s *s3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, workspace.ErrObjectNotFound
		}
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// bytesReader wraps []byte as a ReadSeeker (S3 PutObject requires Seeker).
func bytesReader(b []byte) io.ReadSeeker { return &byteReadSeeker{b: b} }

type byteReadSeeker struct {
	b []byte
	p int64
}

func (r *byteReadSeeker) Read(p []byte) (int, error) {
	if r.p >= int64(len(r.b)) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.p:])
	r.p += int64(n)
	return n, nil
}

func (r *byteReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.p + offset
	case io.SeekEnd:
		abs = int64(len(r.b)) + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("negative position")
	}
	r.p = abs
	return abs, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/... -run TestNewS3Client
```
Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/s3client.go internal/ccappgateway/s3client_test.go
git commit -m "feat(cc-app-gateway): S3 client via aws-sdk-go-v2 default credential chain"
```

---

## Task 4: Add SessionMode to runner.RunInput + BuildArgs switch

**Files:**
- Modify: `internal/ccappgateway/runner/options.go`
- Modify: `internal/ccappgateway/runner/options_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (later tasks):
  - `RunInput.SessionMode string` — `"fresh"` → `--session-id`, `"resume"` → `--resume`, default (`""`) → `--session-id` (preserves Phase 1 callers).
  - `BuildArgs` no longer hardcodes `--session-id`.

- [ ] **Step 1: Add failing tests to options_test.go**

Append to `internal/ccappgateway/runner/options_test.go`:

```go
func TestBuildArgs_SessionMode_Fresh(t *testing.T) {
	args := runner.BuildArgs(runner.RunInput{
		Model:       "claude-haiku-4-5",
		SessionID:   "00000000-0000-0000-0000-000000000001",
		SessionMode: "fresh",
	})
	if !containsAdjacent(args, "--session-id", "00000000-0000-0000-0000-000000000001") {
		t.Errorf("fresh mode: expected --session-id <UUID>; args=%v", args)
	}
	for _, a := range args {
		if a == "--resume" {
			t.Errorf("fresh mode should not include --resume; args=%v", args)
		}
	}
}

func TestBuildArgs_SessionMode_Resume(t *testing.T) {
	args := runner.BuildArgs(runner.RunInput{
		Model:       "claude-haiku-4-5",
		SessionID:   "00000000-0000-0000-0000-000000000001",
		SessionMode: "resume",
	})
	if !containsAdjacent(args, "--resume", "00000000-0000-0000-0000-000000000001") {
		t.Errorf("resume mode: expected --resume <UUID>; args=%v", args)
	}
	for _, a := range args {
		if a == "--session-id" {
			t.Errorf("resume mode should not include --session-id; args=%v", args)
		}
	}
}

func TestBuildArgs_SessionMode_DefaultIsFresh(t *testing.T) {
	args := runner.BuildArgs(runner.RunInput{
		Model:     "claude-haiku-4-5",
		SessionID: "00000000-0000-0000-0000-000000000001",
		// SessionMode left empty
	})
	if !containsAdjacent(args, "--session-id", "00000000-0000-0000-0000-000000000001") {
		t.Errorf("default mode should be --session-id; args=%v", args)
	}
}

// containsAdjacent returns true if args contains a followed by b as
// consecutive elements.
func containsAdjacent(args []string, a, b string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}
```

(If `containsAdjacent` is already defined in the existing test file, reuse it.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/ccappgateway/runner/... -run TestBuildArgs_SessionMode
```
Expected: build error (`RunInput.SessionMode` undefined) OR the existing `--session-id` hardcoded test passes but the new `TestBuildArgs_SessionMode_Resume` fails because resume mode still emits `--session-id`.

- [ ] **Step 3: Modify options.go**

Edit `internal/ccappgateway/runner/options.go`:

Add `SessionMode string` to `RunInput`:

```go
type RunInput struct {
	// ... existing fields ...

	// SessionMode controls which flag carries SessionID:
	//   "fresh"  → --session-id <UUID> (first turn for this session)
	//   "resume" → --resume     <UUID> (subsequent turn — S3 had a prior tarball)
	// Default "" behaves as "fresh" (backward compat for Phase 1 callers).
	SessionMode string

	// ... rest of existing fields ...
}
```

Replace the hardcoded `--session-id` in `BuildArgs`:

```go
func BuildArgs(in RunInput) []string {
	args := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--dangerously-skip-permissions",
		"--model", in.Model,
	}
	switch in.SessionMode {
	case "resume":
		args = append(args, "--resume", in.SessionID)
	default: // "fresh" or empty
		args = append(args, "--session-id", in.SessionID)
	}
	return args
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/runner/...
```
Expected: all options_test cases pass (existing + 3 new).

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/runner/options.go internal/ccappgateway/runner/options_test.go
git commit -m "feat(cc-app-gateway): runner SessionMode switch (--session-id vs --resume)"
```

---

## Task 5: Extend ServeConfig with S3 fields

**Files:**
- Modify: `internal/ccappgateway/config.go`
- Modify: `internal/ccappgateway/config_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (later tasks):
  - `ServeConfig.S3Endpoint, S3Region, S3Bucket string; S3PathStyle bool`
  - Env vars `CCAPPGW_S3_ENDPOINT` (optional), `CCAPPGW_S3_REGION` (required), `CCAPPGW_S3_BUCKET` (required), `CCAPPGW_S3_PATH_STYLE` (bool, optional).

- [ ] **Step 1: Add failing tests to config_test.go**

Append to `internal/ccappgateway/config_test.go`:

```go
func TestLoadServeConfigFromEnv_S3Vars(t *testing.T) {
	// Set all required Phase 1 vars + S3 vars.
	setBaseEnv := func(t *testing.T) {
		t.Setenv("INTERNAL_API_SECRET", "secret")
		t.Setenv("AGENTSERVER_INTERNAL_URL", "http://a:8080")
		t.Setenv("CCAPPGW_LLMPROXY_URL", "http://l:8081")
	}

	t.Run("happy path with all S3 vars", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_ENDPOINT", "http://minio:9000")
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "test-bucket")
		t.Setenv("CCAPPGW_S3_PATH_STYLE", "true")

		cfg, err := ccappgateway.LoadServeConfigFromEnv(ccappgateway.ServeFlags{})
		if err != nil {
			t.Fatalf("LoadServeConfigFromEnv: %v", err)
		}
		if cfg.S3Endpoint != "http://minio:9000" {
			t.Errorf("S3Endpoint: %q", cfg.S3Endpoint)
		}
		if cfg.S3Region != "us-east-1" {
			t.Errorf("S3Region: %q", cfg.S3Region)
		}
		if cfg.S3Bucket != "test-bucket" {
			t.Errorf("S3Bucket: %q", cfg.S3Bucket)
		}
		if !cfg.S3PathStyle {
			t.Errorf("S3PathStyle should be true")
		}
	})

	t.Run("S3_REGION required", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_BUCKET", "b")
		t.Setenv("CCAPPGW_S3_REGION", "")
		_, err := ccappgateway.LoadServeConfigFromEnv(ccappgateway.ServeFlags{})
		if err == nil || !strings.Contains(err.Error(), "CCAPPGW_S3_REGION") {
			t.Errorf("missing S3_REGION should error mentioning the var; got: %v", err)
		}
	})

	t.Run("S3_BUCKET required", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "")
		_, err := ccappgateway.LoadServeConfigFromEnv(ccappgateway.ServeFlags{})
		if err == nil || !strings.Contains(err.Error(), "CCAPPGW_S3_BUCKET") {
			t.Errorf("missing S3_BUCKET should error mentioning the var; got: %v", err)
		}
	})

	t.Run("PATH_STYLE defaults false", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "b")
		t.Setenv("CCAPPGW_S3_PATH_STYLE", "")
		cfg, err := ccappgateway.LoadServeConfigFromEnv(ccappgateway.ServeFlags{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.S3PathStyle {
			t.Errorf("S3PathStyle should default false")
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/ccappgateway/... -run TestLoadServeConfigFromEnv_S3Vars
```
Expected: build error (config fields undefined).

- [ ] **Step 3: Modify config.go**

In `internal/ccappgateway/config.go`, add to `ServeConfig`:

```go
type ServeConfig struct {
	// ... existing Phase 1 fields ...
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3PathStyle bool
}
```

In `LoadServeConfigFromEnv`, after the existing field assignments:

```go
cfg.S3Endpoint = os.Getenv("CCAPPGW_S3_ENDPOINT") // optional
cfg.S3Region = os.Getenv("CCAPPGW_S3_REGION")
if cfg.S3Region == "" {
	return ServeConfig{}, fmt.Errorf("CCAPPGW_S3_REGION required")
}
cfg.S3Bucket = os.Getenv("CCAPPGW_S3_BUCKET")
if cfg.S3Bucket == "" {
	return ServeConfig{}, fmt.Errorf("CCAPPGW_S3_BUCKET required")
}
if v := os.Getenv("CCAPPGW_S3_PATH_STYLE"); v != "" {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return ServeConfig{}, fmt.Errorf("CCAPPGW_S3_PATH_STYLE: %w", err)
	}
	cfg.S3PathStyle = b
}
```

(Add `strconv` import if not present.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/... -run TestLoadServeConfigFromEnv
```
Expected: all subtests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/config.go internal/ccappgateway/config_test.go
git commit -m "feat(cc-app-gateway): config CCAPPGW_S3_* env vars"
```

---

## Task 6: Per-session mutex + S3 readyz on Server; teardownWG drain on Shutdown

**Files:**
- Modify: `internal/ccappgateway/server.go`
- Modify: `internal/ccappgateway/server_test.go`

**Interfaces:**
- Consumes (from Tasks 1, 3, 5): `workspace.ObjectStore`, `NewS3Client`, `ServeConfig.S3*`.
- Produces (Task 7 uses these):
  - `Server.Store workspace.ObjectStore` — populated by `NewServer` via `NewS3Client`.
  - `Server.acquireSessionLock(workspaceID, sessionID string) *sync.Mutex` — returns a per-key mutex, ALREADY LOCKED. Caller MUST release.
  - `Server.teardownWG sync.WaitGroup` — TurnHandler `Add(1)` before backgrounded Teardown; `Done()` after Teardown returns.
  - `Server.Shutdown(ctx)` waits up to ctx deadline for `teardownWG` to drain after `http.Shutdown` returns.
- `Server.NewServerWithRunnerAndStore(cfg, runner, store)` — new test constructor that injects a fake store (alongside existing `NewServerWithRunner`).

- [ ] **Step 1: Add failing tests to server_test.go**

Append to `internal/ccappgateway/server_test.go`:

```go
func TestAcquireSessionLock_SameKeySerializes(t *testing.T) {
	srv, _ := newTestServer(t) // existing helper; if not present, see Step 3 below
	mu1 := srv.AcquireSessionLock("ws", "sid")
	released := make(chan struct{})
	go func() {
		// Second acquire blocks until mu1 is unlocked.
		mu2 := srv.AcquireSessionLock("ws", "sid")
		close(released)
		mu2.Unlock()
	}()
	// Confirm second acquire is blocked.
	select {
	case <-released:
		t.Error("second AcquireSessionLock for same key should block while first held")
	case <-time.After(50 * time.Millisecond):
	}
	mu1.Unlock()
	// Now second should release.
	select {
	case <-released:
	case <-time.After(1 * time.Second):
		t.Error("second AcquireSessionLock did not release after first was unlocked")
	}
}

func TestAcquireSessionLock_DifferentKeysConcurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	mu1 := srv.AcquireSessionLock("ws_a", "sid_1")
	defer mu1.Unlock()
	// Different (workspace, session) → different lock → does not block.
	done := make(chan struct{})
	go func() {
		mu2 := srv.AcquireSessionLock("ws_b", "sid_2")
		close(done)
		mu2.Unlock()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("different keys should not block each other")
	}
}

func TestShutdown_DrainsTeardownWG(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.TeardownWG.Add(1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		srv.TeardownWG.Done()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	// If WG didn't drain, the goroutine above would still be running and srv.Shutdown
	// would have returned via ctx-deadline exceeded — but here we expect clean drain.
}

func TestReadyz_S3Unreachable(t *testing.T) {
	srv, _ := newTestServerWithStore(t, &errorStore{err: errors.New("s3 unreachable")})
	// Existing readyz test asserts 200 when all checks pass; here we expect 503.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz: got %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "s3") {
		t.Errorf("readyz body should mention s3; got %q", rr.Body.String())
	}
}
```

(The helper `newTestServer` exists from Phase 1. Adapt or create `newTestServerWithStore` as needed. `AcquireSessionLock` and `TeardownWG` are exported in this plan via capitalized names for test access — see Step 3.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/ccappgateway/... -run "TestAcquireSessionLock|TestShutdown_DrainsTeardownWG|TestReadyz_S3Unreachable"
```
Expected: build errors (AcquireSessionLock, TeardownWG, S3 readyz check undefined).

- [ ] **Step 3: Modify server.go**

Add fields to `Server`:

```go
import (
	// ... existing ...
	"sync"
)

type Server struct {
	// ... existing Phase 1 fields ...
	Store        workspace.ObjectStore
	SessionLocks sync.Map      // exported for test inspection via AcquireSessionLock helper
	TeardownWG   sync.WaitGroup
}
```

Add helper:

```go
// AcquireSessionLock returns a per-(workspaceID, sessionID) mutex already
// locked. Caller MUST Unlock when done (typically inside the Teardown
// goroutine after S3 Put completes).
func (s *Server) AcquireSessionLock(workspaceID, sessionID string) *sync.Mutex {
	key := workspaceID + "/" + sessionID
	actual, _ := s.SessionLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu
}
```

Modify `NewServer` to construct the S3 client:

```go
func NewServer(cfg ServeConfig) (*Server, error) {
	ctx := context.Background()
	store, err := NewS3Client(ctx, S3Config{
		Endpoint:  cfg.S3Endpoint,
		Region:    cfg.S3Region,
		Bucket:    cfg.S3Bucket,
		PathStyle: cfg.S3PathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("server: s3 client init: %w", err)
	}
	return newServerInternal(cfg, runner.Run, store), nil
}

func NewServerWithRunner(cfg ServeConfig, fakeRunner RunnerFunc) (*Server, error) {
	// In tests, default to a no-op store if not specified.
	return NewServerWithRunnerAndStore(cfg, fakeRunner, &noopStore{})
}

func NewServerWithRunnerAndStore(cfg ServeConfig, fakeRunner RunnerFunc, store workspace.ObjectStore) (*Server, error) {
	return newServerInternal(cfg, fakeRunner, store), nil
}

func newServerInternal(cfg ServeConfig, r RunnerFunc, store workspace.ObjectStore) *Server {
	s := &Server{
		// ... wire existing fields ...
		Store: store,
	}
	s.handler = &TurnHandler{
		Cfg:     cfg,
		WSToken: NewWSTokenClient(cfg.AgentserverInternalURL, cfg.InternalSecret),
		Runner:  r,
		TmpRoot: cfg.TmpRoot,
		Store:   store,    // Task 7 will add this field
		Server:  s,        // Task 7 will add this field for AcquireSessionLock access
	}
	return s
}

// noopStore is used by NewServerWithRunner default — tests that don't
// exercise Setup/Teardown can use this. Real tests inject via NewServerWithRunnerAndStore.
type noopStore struct{}

func (noopStore) Put(_ context.Context, _ string, _ []byte) error  { return nil }
func (noopStore) Get(_ context.Context, _ string) ([]byte, error)  { return nil, workspace.ErrObjectNotFound }
func (noopStore) Delete(_ context.Context, _ string) error          { return nil }
```

Extend the readyz handler. Find the existing readyz logic and add the S3 probe:

```go
// Existing checks: claude binary, agentserver reachability.
// New: S3 reachability via probe key.
const s3ProbeKey = "cc-app-gateway/__readyz__/probe"
probeCtx, probeCancel := context.WithTimeout(r.Context(), 3*time.Second)
defer probeCancel()
_, err := s.Store.Get(probeCtx, s3ProbeKey)
if err != nil && !errors.Is(err, workspace.ErrObjectNotFound) {
	failures = append(failures, "s3 unreachable: "+err.Error())
}
```

Extend `Shutdown` to drain `teardownWG`:

```go
func (s *Server) Shutdown(ctx context.Context) error {
	httpErr := s.http.Shutdown(ctx)

	// Wait for in-flight Teardown goroutines with the same deadline.
	done := make(chan struct{})
	go func() {
		s.TeardownWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("[cc-app-gateway] shutdown deadline reached with pending teardowns")
	}
	return httpErr
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/... -run "TestAcquireSessionLock|TestShutdown_DrainsTeardownWG|TestReadyz_S3Unreachable"
```
Expected: 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/server.go internal/ccappgateway/server_test.go
git commit -m "feat(cc-app-gateway): per-session mutex + S3 readyz + Shutdown drain"
```

---

## Task 7: Wire S3 + mutex + SessionMode into TurnHandler

**Files:**
- Modify: `internal/ccappgateway/turn_api.go`
- Modify: `internal/ccappgateway/turn_api_test.go`

**Interfaces:**
- Consumes (from Tasks 1-6): `workspace.ObjectStore`, `Workspace.IsResume`, new `Setup(ctx, tmpRoot, wid, sid, store)` signature, new `(*Workspace) Teardown(ctx, store) error`, `RunInput.SessionMode`, `Server.AcquireSessionLock`, `Server.TeardownWG`.
- Produces: same `CcTurnRequest`/`CcTurnResponse` shapes from Phase 1 (no wire change). Behavior change only.

- [ ] **Step 1: Add failing tests to turn_api_test.go**

Append to `internal/ccappgateway/turn_api_test.go`:

```go
func TestServeHTTP_ResumeOnPriorTarball(t *testing.T) {
	// Seed store with a prior tarball for this (workspace, session).
	store := newFakeStore()
	seedDir := t.TempDir()
	os.MkdirAll(filepath.Join(seedDir, "projects", "-tmp-x"), 0o700)
	os.WriteFile(filepath.Join(seedDir, "projects", "-tmp-x", "00000000-0000-0000-0000-000000000001.jsonl"), []byte("seed\n"), 0o600)
	workspace.TarUpload(context.Background(), store, "cc-app-gateway/ws_test/00000000-0000-0000-0000-000000000001.tar.gz", seedDir)

	// Fake runner captures the SessionMode it received.
	var gotMode string
	fakeRunner := func(_ context.Context, in runner.RunInput) (*runner.RunResult, error) {
		gotMode = in.SessionMode
		return &runner.RunResult{
			AssistantText: "ok",
			Meta:          &runner.ResultMeta{Subtype: "success"},
		}, nil
	}

	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)
	rr := postTurn(t, srv, `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", rr.Code, rr.Body.String())
	}
	if gotMode != "resume" {
		t.Errorf("expected SessionMode=resume on prior tarball; got %q", gotMode)
	}
}

func TestServeHTTP_FreshSessionMode(t *testing.T) {
	store := newFakeStore() // empty
	var gotMode string
	fakeRunner := func(_ context.Context, in runner.RunInput) (*runner.RunResult, error) {
		gotMode = in.SessionMode
		return &runner.RunResult{AssistantText: "ok", Meta: &runner.ResultMeta{Subtype: "success"}}, nil
	}
	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)
	rr := postTurn(t, srv, `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if gotMode != "fresh" {
		t.Errorf("expected SessionMode=fresh on empty store; got %q", gotMode)
	}
}

func TestServeHTTP_SameSessionSecondTurnBlocks(t *testing.T) {
	store := newFakeStore()
	// Slow runner so we can observe the second turn blocking on the mutex.
	runnerEntered := make(chan struct{})
	runnerRelease := make(chan struct{})
	fakeRunner := func(_ context.Context, _ runner.RunInput) (*runner.RunResult, error) {
		runnerEntered <- struct{}{}
		<-runnerRelease
		return &runner.RunResult{AssistantText: "ok", Meta: &runner.ResultMeta{Subtype: "success"}}, nil
	}
	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)

	// First call (in goroutine — will block on runnerRelease)
	body := `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`
	go postTurn(t, srv, body)
	<-runnerEntered

	// Second call (should block on AcquireSessionLock because Teardown still holds mutex)
	secondReturned := make(chan struct{})
	go func() {
		postTurn(t, srv, body)
		close(secondReturned)
	}()
	select {
	case <-secondReturned:
		t.Error("second turn should not return while first turn's mutex is held")
	case <-time.After(100 * time.Millisecond):
	}

	// Release first runner; first will complete, Teardown will run and release mutex.
	close(runnerRelease)
	// Now second call can proceed — give the test the same release signal next time.
	// (For this test we let it complete naturally; the assertion is that it was blocked.)
}

func TestServeHTTP_S3GetFailsNonNotFound_Returns502(t *testing.T) {
	store := &errorStore{err: errors.New("network unreachable")}
	srv := newTestServerWithStoreAndRunner(t, store, nil) // runner won't be called
	rr := postTurn(t, srv, `{"workspaceId":"ws","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("S3 non-NotFound err should map to 500; got %d", rr.Code)
	}
	var resp struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Code != "workspace_setup_failed" {
		t.Errorf("expected code=workspace_setup_failed; got %q", resp.Code)
	}
}
```

(Helpers `newTestServerWithStoreAndRunner`, `postTurn` may need to be added — see existing Phase 1 patterns.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -v ./internal/ccappgateway/... -run "TestServeHTTP_Resume|TestServeHTTP_FreshSession|TestServeHTTP_SameSession|TestServeHTTP_S3Get"
```
Expected: build errors (TurnHandler.Store/Server fields undefined; new Setup signature mismatch).

- [ ] **Step 3: Modify turn_api.go**

Add fields to `TurnHandler`:

```go
type TurnHandler struct {
	// ... existing Phase 1 fields ...
	Store  workspace.ObjectStore
	Server *Server // for AcquireSessionLock + TeardownWG access
}
```

Replace the workspace-setup section of `ServeHTTP`. Find the existing `ws, err := workspace.Setup(r.Context(), h.TmpRoot)` and replace with:

```go
// Acquire per-session mutex to serialize turns for the same (workspace, session)
// within this pod. Released by the Teardown goroutine after S3 Put completes
// (or after Teardown errors). See spec § Concurrency.
mu := h.Server.AcquireSessionLock(req.WorkspaceID, req.SessionID)
mutexReleased := false
defer func() {
	if !mutexReleased {
		// Only fires if Setup itself failed (Teardown never ran → goroutine never started).
		mu.Unlock()
	}
}()

ws, err := workspace.Setup(r.Context(), h.TmpRoot, req.WorkspaceID, req.SessionID, h.Store)
if err != nil {
	log.Printf("[cc-app-gateway] workspace_setup_failed (session=%s): %v", req.SessionID, err)
	writeError(w, http.StatusInternalServerError, "workspace_setup_failed", "workspace setup failed")
	return
}

// Background Teardown — releases the mutex AFTER S3 Put completes.
defer func() {
	h.Server.TeardownWG.Add(1)
	mutexReleased = true // tell the outer defer not to unlock again
	go func() {
		defer h.Server.TeardownWG.Done()
		defer mu.Unlock()
		bctx, bcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bcancel()
		if err := ws.Teardown(bctx, h.Store); err != nil {
			log.Printf("[cc-app-gateway] workspace teardown failed (session=%s): %v", req.SessionID, err)
		}
	}()
}()
```

Pick `SessionMode` from `ws.IsResume` and pass to runner:

```go
sessionMode := "fresh"
if ws.IsResume {
	sessionMode = "resume"
}

result, err := h.Runner(runCtx, runner.RunInput{
	// ... existing fields ...
	SessionMode: sessionMode,
})
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/ccappgateway/...
```
Expected: all turn_api_test cases pass (existing + 4 new).

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/turn_api.go internal/ccappgateway/turn_api_test.go
git commit -m "feat(cc-app-gateway): turn_api wires S3 store, per-session mutex, SessionMode"
```

---

## Task 8: Helm chart + values.yaml S3 wiring

**Files:**
- Modify: `deploy/helm/agentserver/templates/cc-app-gateway.yaml`
- Modify: `deploy/helm/agentserver/values.yaml`

No tests (helm is verified manually + smoke-checked).

- [ ] **Step 1: Add S3 block to values.yaml**

In `deploy/helm/agentserver/values.yaml`, find the `ccAppGateway:` block (added in Phase 1) and add:

```yaml
ccAppGateway:
  # ... existing Phase 1 fields ...
  s3:
    endpoint: ""           # MinIO endpoint URL; empty for real AWS
    region: ""             # REQUIRED when enabled=true
    bucket: ""             # REQUIRED when enabled=true
    pathStyle: false       # true for MinIO
    existingSecret: ""     # k8s secret with keys: access_key_id, secret_access_key
```

- [ ] **Step 2: Add CCAPPGW_S3_* env vars to cc-app-gateway.yaml**

In `deploy/helm/agentserver/templates/cc-app-gateway.yaml`, inside the Deployment's `env:` block (next to existing CCAPPGW_* vars):

```yaml
{{- if .Values.ccAppGateway.enabled }}
        - name: CCAPPGW_S3_ENDPOINT
          value: {{ .Values.ccAppGateway.s3.endpoint | quote }}
        - name: CCAPPGW_S3_REGION
          value: {{ required "ccAppGateway.s3.region required when enabled=true" .Values.ccAppGateway.s3.region | quote }}
        - name: CCAPPGW_S3_BUCKET
          value: {{ required "ccAppGateway.s3.bucket required when enabled=true" .Values.ccAppGateway.s3.bucket | quote }}
        - name: CCAPPGW_S3_PATH_STYLE
          value: {{ .Values.ccAppGateway.s3.pathStyle | quote }}
{{- end }}
```

After the `env:` block, add `envFrom` to wire AWS creds from a Secret (if specified):

```yaml
{{- if .Values.ccAppGateway.s3.existingSecret }}
      envFrom:
        - secretRef:
            name: {{ .Values.ccAppGateway.s3.existingSecret }}
{{- end }}
```

The secret should contain `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` keys. For EKS with IRSA, leave `existingSecret: ""` and use a ServiceAccount annotation instead (operator's responsibility — Phase 2 doesn't auto-wire IRSA).

- [ ] **Step 3: Smoke-test the chart**

```bash
cd deploy/helm/agentserver
# Default values (ccAppGateway.enabled=false) should not require S3 vars.
helm template . > /tmp/chart-default.yaml
grep -c "cc-app-gateway" /tmp/chart-default.yaml
# Expected: 0 (template only renders when enabled=true)

# Enabling without S3 vars should fail with the required error.
helm template . --set ccAppGateway.enabled=true 2>&1 | head -5
# Expected: error mentioning "ccAppGateway.s3.region required"

# Enabling with S3 vars should succeed.
helm template . \
  --set ccAppGateway.enabled=true \
  --set ccAppGateway.s3.region=us-east-1 \
  --set ccAppGateway.s3.bucket=test \
  > /tmp/chart-enabled.yaml
grep -c "CCAPPGW_S3_REGION" /tmp/chart-enabled.yaml
# Expected: ≥1

helm lint .
# Expected: 0 failures
```

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/agentserver/templates/cc-app-gateway.yaml deploy/helm/agentserver/values.yaml
git commit -m "feat(cc-app-gateway): helm chart S3 env vars + required-gate"
```

---

## Task 9: Integration test — minio sidecar + resume across turns

**Files:**
- Modify: `internal/ccappgateway/testdata/integration/docker-compose.yml`
- Modify: `cmd/cc-app-gateway-test-tools/main.go` (add `--log-requests-to` flag to fake-llmproxy)
- Modify: `internal/ccappgateway/integration_test.go` (add `TestIntegration_ResumeAcrossTurns`)

**Interfaces:**
- Consumes: everything from Tasks 1-8.
- Produces: a passing end-to-end resume test against real claude binary + minio.

- [ ] **Step 1: Add minio + minio-init to docker-compose.yml**

Append to `internal/ccappgateway/testdata/integration/docker-compose.yml`:

```yaml
  minio:
    image: minio/minio:latest
    command: server /data
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 2s
      timeout: 1s
      retries: 10

  minio-init:
    image: minio/mc:latest
    depends_on:
      minio:
        condition: service_healthy
    entrypoint: >
      sh -c "
        mc alias set local http://minio:9000 minioadmin minioadmin &&
        mc mb -p local/cc-app-gateway-test
      "
```

Modify the cc-app-gateway service to depend on `minio-init` and pass S3 env vars:

```yaml
  cc-app-gateway:
    # ... existing ...
    depends_on:
      fake-agentserver:
        condition: service_healthy
      fake-llmproxy:
        condition: service_started
      minio-init:
        condition: service_completed_successfully
    environment:
      # ... existing CCAPPGW_* + INTERNAL_API_SECRET ...
      CCAPPGW_S3_ENDPOINT: http://minio:9000
      CCAPPGW_S3_REGION: us-east-1
      CCAPPGW_S3_BUCKET: cc-app-gateway-test
      CCAPPGW_S3_PATH_STYLE: "true"
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
```

Modify the fake-llmproxy service to enable request logging:

```yaml
  fake-llmproxy:
    # ... existing ...
    command: fake-llmproxy --listen :8081 --accept-token deadbeef --canned-reply "pong" --log-requests-to /tmp/llmproxy-requests.log
    volumes:
      - llmproxy-logs:/tmp

volumes:
  llmproxy-logs:
```

- [ ] **Step 2: Add `--log-requests-to` to fake-llmproxy**

In `cmd/cc-app-gateway-test-tools/main.go`, find the `fake-llmproxy` subcommand. Add a flag:

```go
logRequestsTo := fakeLLMProxyFlags.String("log-requests-to", "", "if set, append every inbound request body to this file as JSON lines")
```

In the `POST /v1/messages` handler, before processing, append the body to the log file (if `--log-requests-to` set):

```go
mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	if *logRequestsTo != "" {
		f, err := os.OpenFile(*logRequestsTo, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			f.Write(body)
			f.Write([]byte("\n"))
			f.Close()
		}
	}

	// ... rest of existing handler ...
})
```

- [ ] **Step 3: Add TestIntegration_ResumeAcrossTurns**

Append to `internal/ccappgateway/integration_test.go`:

```go
func TestIntegration_ResumeAcrossTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	abs, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatal(err)
	}
	runMake(t, abs, "up")
	t.Cleanup(func() { runMakeBestEffort(t, abs, "down") })
	waitForReadyz(t, gatewayURL+"/readyz", 90*time.Second)

	sessionID := "00000000-0000-4000-8000-00000000beef"

	// Turn 1: tell claude a fact.
	turn1Body := fmt.Sprintf(`{
		"workspaceId": "ws_resume_test",
		"sessionId":   %q,
		"userMessage": "Remember this code: ALPHA-7."
	}`, sessionID)
	resp1 := doTurnRequest(t, turn1Body)
	if resp1.StatusCode != 200 {
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("turn 1 status: %d", resp1.StatusCode)
	}

	// Turn 2: same sessionID — claude should send the conversation history including turn 1.
	turn2Body := fmt.Sprintf(`{
		"workspaceId": "ws_resume_test",
		"sessionId":   %q,
		"userMessage": "Recall the code."
	}`, sessionID)
	resp2 := doTurnRequest(t, turn2Body)
	if resp2.StatusCode != 200 {
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("turn 2 status: %d", resp2.StatusCode)
	}

	// Inspect fake-llmproxy's request log: turn 2's request body MUST include
	// turn 1's user message text "ALPHA-7", proving claude resumed and sent
	// the full conversation history (not that the LLM "remembers" — we use a
	// fake LLM).
	logContent := readFakeLLMProxyLog(t, abs)
	// Split into per-request entries; the second-to-last request body is turn 2's
	// /v1/messages call (after the OAuth/telemetry pre-flights from turn 1).
	if !strings.Contains(logContent, "ALPHA-7") {
		runMakeBestEffort(t, abs, "logs")
		t.Errorf("fake-llmproxy log should contain 'ALPHA-7' from turn 1 history; log content:\n%s", logContent)
	}
}

func doTurnRequest(t *testing.T, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", gatewayURL+"/api/turns", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "secret123")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("turn request: %v", err)
	}
	return resp
}

func readFakeLLMProxyLog(t *testing.T, testdataAbs string) string {
	t.Helper()
	// Use `docker compose exec` to read the log file from inside the container.
	cmd := exec.Command("docker", "compose", "-f", filepath.Join(testdataAbs, "docker-compose.yml"),
		"exec", "-T", "fake-llmproxy", "cat", "/tmp/llmproxy-requests.log")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("read log: %v (output: %s)", err, out)
		return ""
	}
	return string(out)
}
```

- [ ] **Step 4: Run the integration test**

```bash
go test -tags integration -v -timeout 10m -run TestIntegration_ResumeAcrossTurns ./internal/ccappgateway/...
```

Expected: PASS. If it fails, common causes:
- minio-init never completed: check `docker compose -f .../docker-compose.yml logs minio-init`
- cc-app-gateway readyz never green: probe key may be wrong (verify `cc-app-gateway/__readyz__/probe` consistent across Task 6 + spec)
- ALPHA-7 not in log: read the log directly via `docker compose exec fake-llmproxy cat /tmp/llmproxy-requests.log` and diagnose what claude actually sent.

- [ ] **Step 5: Commit**

```bash
git add internal/ccappgateway/testdata/integration/docker-compose.yml \
        cmd/cc-app-gateway-test-tools/main.go \
        internal/ccappgateway/integration_test.go
git commit -m "test(cc-app-gateway): integration test for session resume via minio"
```

---

## Final pass

- [ ] **Run full test suite from repo root**

```bash
go test ./...                                          # all unit tests
go test -tags integration ./internal/ccappgateway/...  # integration (both happy-path + resume)
go vet ./...
```

- [ ] **Update memory**

Add a Phase-2-complete note to `/root/.claude/projects/-root-agentserver/memory/` cross-linking with the Phase 1 memory.

- [ ] **Open PR (stacked on Phase 1's PR #279)**

```bash
gh pr create --base feat/cc-app-gateway-phase1 \
             --head feat/cc-app-gateway-phase2 \
             --title "feat(cc-app-gateway): Phase 2 — S3 workspace persistence + session resume" \
             --body "$(cat ../PR-PHASE-2-BODY.md)"
```

PR body must:
- Mention this is Phase 2 stacked on Phase 1's PR #279 (won't merge until #279 lands)
- Link spec + plan
- Call out the 3 critical + 2 important spec audit revisions (CLAUDE_CODE_AUTO_COMPACT_WINDOW was wrong, backups/ pruning added, per-session mutex added, readyz probe key inside prefix, default credential chain not static)
- Show integration test PASS output (both happy-path from Phase 1 and resume test from Phase 2)

- [ ] **Bump chart (post-merge of both PRs)**

Per `agentserver_release_flow` memory: bump `Chart.yaml` minor version, push `v<version>` git tag.

---

## Out-of-band: rerun Phase 0 probe before starting Phase 2 implementation

The cwd-sanitization algorithm in claude 2.1.185 (`/` → `-`, strip leading `-`) is load-bearing for resume (spec Open Risk #3). Before starting Task 2, run a quick sanity probe to confirm the algorithm hasn't drifted:

```bash
cd /tmp/cc-probe && ./probe fresh 'echo hi'
ls /tmp/cc-probe/claude-home/projects/
# Expected: a directory named "-tmp-cc-probe" (i.e. cwd "/tmp/cc-probe" sanitized)
```

If the algorithm drifted (e.g., now hashes instead of `/`-replaces), STOP. Update spec § Architecture Critical correctness invariants #1 and pick a new ProjectDir layout strategy. Don't push forward on Phase 2 with a broken resume assumption.

---

## Self-review (run after writing this plan)

Done as part of writing. Checks performed:

1. **Spec coverage:** Every § Component changes item in the spec has a corresponding task. Concurrency (§ Concurrency) is Task 6. S3 wiring (§ Component changes / s3client) is Task 3. ProjectDir invariant (§ Critical correctness invariants) is Task 2. Backups pruning (§ Audit Revision #2) is Task 2 (Teardown step 1). readyz probe key inside prefix (§ Audit Revision #4) is Task 6. IRSA-friendly creds (§ Audit Revision #5) is Task 3. Sessions resume integration test (§ Tests / Integration) is Task 9.

2. **Placeholder scan:** No "TBD", "TODO", "fill in details", "appropriate error handling". Every step has either code or an exact command.

3. **Type consistency:**
   - `ObjectStore` interface defined in Task 1; consumed by Tasks 2, 3, 6, 7.
   - `Workspace` fields (TempDir, ClaudeDir, ProjectDir, IsResume) consistent across Tasks 2 and 7.
   - `Setup(ctx, tmpRoot, workspaceID, sessionID, store)` signature consistent between Task 2 definition and Task 7 caller.
   - `Teardown(ctx, store) error` signature consistent.
   - `SessionMode` string values `"fresh"` / `"resume"` consistent between Task 4 (runner) and Task 7 (caller).
   - `Server.AcquireSessionLock` / `TeardownWG` / `Store` field names consistent between Task 6 (definition) and Task 7 (caller).
   - S3 key format `cc-app-gateway/<workspaceID>/<sessionID>.tar.gz` consistent across Tasks 2 (`s3Key` helper), 6 (readyz probe key `cc-app-gateway/__readyz__/probe`), 9 (integration test).
