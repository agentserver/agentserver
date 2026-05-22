// internal/codexappgateway/scheduler/spawn_test.go
package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSpawnExec_BinExitsZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecodex")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nread input\necho \"hello $input\"\n"), 0o755))

	s := NewSpawner(bin, nil)
	res, err := s.Run(context.Background(), SpawnInput{
		Prompt:  "world",
		Env:     []string{"TZ=UTC"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if res.Transcript == "" {
		t.Fatalf("empty transcript")
	}
}

func TestSpawnExec_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "slowcodex")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 10\n"), 0o755))

	s := NewSpawner(bin, nil)
	res, err := s.Run(context.Background(), SpawnInput{
		Prompt:  "x",
		Timeout: 200 * time.Millisecond,
	})
	if err == nil && res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit; got %+v", res)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true; got %+v", res)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
