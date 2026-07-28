package codex_test

import (
	"bufio"
	"embed"
	"io/fs"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

//go:embed fixtures/dialect/*.jsonl
var dialectFixtures embed.FS

func TestDialectFixturesAreValidCodexWireMessages(t *testing.T) {
	paths, err := fs.Glob(dialectFixtures, "fixtures/dialect/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no dialect fixtures found")
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			file, err := dialectFixtures.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			line := 0
			for scanner.Scan() {
				line++
				if _, err := codexwire.Parse(scanner.Bytes()); err != nil {
					t.Errorf("line %d: %v", line, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if line == 0 {
				t.Fatal("fixture is empty")
			}
		})
	}
}
