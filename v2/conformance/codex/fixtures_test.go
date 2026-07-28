package codex_test

import (
	"bufio"
	"embed"
	"io/fs"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

//go:embed fixtures/appserver/*.jsonl fixtures/dialect/*.jsonl fixtures/execserver/*.jsonl
var wireFixtures embed.FS

func TestCodexWireFixturesAreValid(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"fixtures/appserver/*.jsonl", "fixtures/dialect/*.jsonl", "fixtures/execserver/*.jsonl"} {
		matches, err := fs.Glob(wireFixtures, pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no Codex wire fixtures found")
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			file, err := wireFixtures.Open(path)
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
