package codex_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
)

const (
	maxA08StateFiles     = 512
	maxA08StateFileBytes = 16 * 1024 * 1024
	maxA08StateTreeBytes = 64 * 1024 * 1024
	maxA08RolloutLine    = 4 * 1024 * 1024
)

type stateFileSnapshot struct {
	Mode   os.FileMode
	Size   int64
	SHA256 string
}

// TestAppServerA08GracefulShutdownStabilizesState establishes stdin EOF plus
// clean process exit as the persistence barrier. The probe never snapshots a
// live CODEX_HOME and uses no fixed sleep: after Wait reports a zero exit, two
// bounded tree reads must agree byte-for-byte, the reported rollout must be a
// complete JSONL file, and the state database must have a SQLite header.
func TestAppServerA08GracefulShutdownStabilizesState(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	response, err := scriptedmodel.AssistantMessage(
		"response-a08-graceful-shutdown",
		"message-a08-graceful-shutdown",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{response},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(
		t,
		collector,
		paths.cwd,
		"complete a persisted turn before graceful shutdown",
	)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")

	// This turn issues no server requests, so the worker's outstanding set is
	// empty at terminal. EOF and Wait are the only persistence barrier.
	closeAndWait(t, process)

	first := snapshotStateTree(t, paths.codexHome)
	second := snapshotStateTree(t, paths.codexHome)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("CODEX_HOME changed after clean process exit:\nfirst=%v\nsecond=%v", first, second)
	}
	if len(first) == 0 {
		t.Fatal("clean app-server exit left an empty CODEX_HOME")
	}

	rolloutRelative := stateRelativePath(t, paths.codexHome, thread.Thread.Path)
	rollout, exists := first[rolloutRelative]
	if !exists {
		t.Fatalf("thread rollout %q is absent from the stable state tree", rolloutRelative)
	}
	if !strings.HasSuffix(rolloutRelative, ".jsonl") || rollout.Size == 0 {
		t.Fatalf("thread rollout snapshot = %+v at %q, want non-empty JSONL", rollout, rolloutRelative)
	}
	assertCompleteRolloutJSONL(
		t,
		thread.Thread.Path,
		thread.Thread.ID,
		"complete a persisted turn before graceful shutdown",
		conformanceFinalText,
	)

	stateDatabase := filepath.Join(paths.codexHome, "state_5.sqlite")
	stateRelative := stateRelativePath(t, paths.codexHome, stateDatabase)
	if _, exists := first[stateRelative]; !exists {
		t.Fatalf("stable state tree omitted %q", stateRelative)
	}
	assertSQLiteHeader(t, stateDatabase)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	if requests := modelServer.Requests(); len(requests) != 1 {
		t.Fatalf("scripted model received %d requests, want one", len(requests))
	}
	t.Logf("A08 stable post-exit state: %s", formatStateTree(first))
}

func snapshotStateTree(t *testing.T, root string) map[string]stateFileSnapshot {
	t.Helper()
	root = filepath.Clean(root)
	result := make(map[string]stateFileSnapshot)
	var totalBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize state path %q: %w", path, err)
		}
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("state path escaped root: %q", relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("state tree contains symlink %q", relative)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat state file %q: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state tree contains non-regular file %q with mode %s", relative, info.Mode())
		}
		if info.Size() < 0 || info.Size() > maxA08StateFileBytes {
			return fmt.Errorf("state file %q has size %d outside [0, %d]", relative, info.Size(), maxA08StateFileBytes)
		}
		totalBytes += info.Size()
		if totalBytes > maxA08StateTreeBytes {
			return fmt.Errorf("state tree exceeds %d bytes", maxA08StateTreeBytes)
		}
		if len(result) >= maxA08StateFiles {
			return fmt.Errorf("state tree exceeds %d files", maxA08StateFiles)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read state file %q: %w", relative, err)
		}
		if int64(len(contents)) != info.Size() {
			return fmt.Errorf("state file %q changed size while snapshotting: stat=%d read=%d", relative, info.Size(), len(contents))
		}
		digest := sha256.Sum256(contents)
		result[filepath.ToSlash(relative)] = stateFileSnapshot{
			Mode:   info.Mode().Perm(),
			Size:   info.Size(),
			SHA256: hex.EncodeToString(digest[:]),
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot stable CODEX_HOME: %v", err)
	}
	return result
}

func stateRelativePath(t *testing.T, root, path string) string {
	t.Helper()
	if path == "" || !filepath.IsAbs(path) {
		t.Fatalf("state path must be absolute and non-empty: %q", path)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve CODEX_HOME %q: %v", root, err)
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve state path %q: %v", path, err)
	}
	relative, err := filepath.Rel(filepath.Clean(canonicalRoot), filepath.Clean(canonicalPath))
	if err != nil {
		t.Fatalf("relativize state path %q: %v", path, err)
	}
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("state path %q escaped CODEX_HOME %q", path, root)
	}
	return filepath.ToSlash(relative)
}

func assertCompleteRolloutJSONL(t *testing.T, path string, required ...string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxA08RolloutLine)
	lineCount := 0
	var contents bytes.Buffer
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 || !json.Valid(line) {
			t.Fatalf("rollout line %d is not one non-empty JSON value", lineCount+1)
		}
		lineCount++
		contents.Write(line)
		contents.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan rollout: %v", err)
	}
	if lineCount == 0 {
		t.Fatal("rollout contains no JSONL records")
	}
	for _, value := range required {
		if value == "" || !bytes.Contains(contents.Bytes(), []byte(value)) {
			t.Fatalf("rollout omitted required completed-turn value %q", value)
		}
	}
}

func assertSQLiteHeader(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SQLite state database: %v", err)
	}
	if len(contents) < 100 || !bytes.Equal(contents[:16], []byte("SQLite format 3\x00")) {
		t.Fatalf("state database %q lacks a valid SQLite format header", path)
	}
}

func formatStateTree(snapshot map[string]stateFileSnapshot) string {
	paths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		file := snapshot[path]
		entries = append(entries, fmt.Sprintf("%s(%d,%s)", path, file.Size, file.SHA256[:12]))
	}
	return strings.Join(entries, ", ")
}
