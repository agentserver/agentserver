package finalexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsImplicitOrPrivilegedBoundary(t *testing.T) {
	program := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(program, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	base := Config{
		Program:     program,
		Directory:   directory,
		Environment: []string{},
		ExpectedUID: 65532,
		ExpectedGID: 65532,
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "relative program", mutate: func(c *Config) { c.Program = "codex" }, want: "absolute"},
		{name: "implicit environment", mutate: func(c *Config) { c.Environment = nil }, want: "explicit"},
		{name: "duplicate environment", mutate: func(c *Config) { c.Environment = []string{"A=1", "A=2"} }, want: "duplicate"},
		{name: "root uid", mutate: func(c *Config) { c.ExpectedUID = 0 }, want: "unprivileged"},
		{name: "reserved uid", mutate: func(c *Config) { c.ExpectedUID = ^uint32(0) }, want: "valid"},
		{name: "stdio trap", mutate: func(c *Config) { c.RequiredOpenFDs = []int{2} }, want: "stdio"},
		{name: "duplicate trap", mutate: func(c *Config) { c.RequiredOpenFDs = []int{9, 9} }, want: "duplicated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.mutate(&config)
			if err := validate(config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate() error = %v, want %q", err, test.want)
			}
		})
	}
	if err := validate(base); err != nil {
		t.Fatalf("validate(valid) error = %v", err)
	}

	invalidEnvironment := base
	invalidEnvironment.Environment = []string{"TOKEN=must-not-be-logged\x00"}
	if err := validate(invalidEnvironment); err == nil || strings.Contains(err.Error(), "must-not-be-logged") {
		t.Fatalf("validate(invalid environment) error = %v, want redacted rejection", err)
	}
}
