package harnessworker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestLoadPromptVerifiesSignedPointerAndClosesPipe(t *testing.T) {
	contents := []byte("run the exact requested task")
	pointer := promptTestPointer(contents)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := writer.Write(contents)
		writeDone <- errors.Join(writeErr, writer.Close())
	}()
	prompt, err := LoadPrompt(reader, pointer)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if prompt != string(contents) {
		t.Fatalf("prompt = %q", prompt)
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("LoadPrompt left the inherited pipe open")
	}
}

func TestLoadPromptRejectsWrongTypeSizeDigestAndDescriptor(t *testing.T) {
	contents := []byte("model-visible input")
	base := promptTestPointer(contents)
	tests := []struct {
		name   string
		mutate func(*runmanifest.ObjectPointer)
		data   []byte
		want   string
	}{
		{name: "media type", mutate: func(p *runmanifest.ObjectPointer) { p.MediaType = "application/json" }, data: contents, want: "media type"},
		{name: "size", mutate: func(p *runmanifest.ObjectPointer) { p.SizeBytes++ }, data: contents, want: "size"},
		{name: "digest", mutate: func(p *runmanifest.ObjectPointer) { p.SHA256 = strings.Repeat("0", 64) }, data: contents, want: "digest"},
		{name: "invalid UTF-8", mutate: func(p *runmanifest.ObjectPointer) { *p = promptTestPointer([]byte{0xff}) }, data: []byte{0xff}, want: "UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pointer := base
			test.mutate(&pointer)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				_, _ = writer.Write(test.data)
				_ = writer.Close()
			}()
			_, err = LoadPrompt(reader, pointer)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadPrompt() error = %v, want %q", err, test.want)
			}
		})
	}

	file, err := os.CreateTemp(t.TempDir(), "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrompt(file, base); err == nil || !strings.Contains(err.Error(), "must be a pipe") {
		t.Fatalf("non-pipe error = %v", err)
	}
	if _, err := file.Stat(); err == nil {
		t.Fatal("rejected prompt descriptor remained open")
	}
}

func TestLoadPromptRejectsOversizeBeforeReading(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	pointer := promptTestPointer([]byte("x"))
	pointer.SizeBytes = MaximumPromptBytes + 1
	if _, err := LoadPrompt(reader, pointer); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversize prompt error = %v", err)
	}
	if _, err := io.WriteString(writer, "closed"); err == nil {
		t.Fatal("oversize prompt rejection did not close the read side")
	}
}

func promptTestPointer(contents []byte) runmanifest.ObjectPointer {
	digest := sha256.Sum256(contents)
	return runmanifest.ObjectPointer{
		ObjectID: "91000000-0000-4000-8000-000000000091",
		SHA256:   hex.EncodeToString(digest[:]), SizeBytes: int64(len(contents)), MediaType: PromptMediaType,
	}
}
