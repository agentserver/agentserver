package tooloutput

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func wireJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func chunk(seq int, stream string, data []byte) map[string]any {
	return map[string]any{"sequence": seq, "stream": stream, "chunk_base64": base64.StdEncoding.EncodeToString(data)}
}
func shell(chunks ...map[string]any) map[string]any {
	return map[string]any{"process_id": "p1", "status": "succeeded", "exit_code": 0, "chunks": chunks, "next_sequence": len(chunks) + 1, "sandbox_denied": false, "timed_out": false, "output_complete": true}
}

func TestShellJoinsUTF8PerStreamBeforeDecoding(t *testing.T) {
	data := []byte("你好\n")
	wire := shell(chunk(1, "stdout", data[:1]), chunk(2, "stderr", []byte("warning\n")), chunk(3, "stdout", data[1:4]), chunk(4, "stdout", data[4:]), chunk(5, "pty", []byte("terminal\n")))
	text, known, err := Render("executor", "shell", wireJSON(t, wire), 4096)
	if err != nil || !known {
		t.Fatal(known, err)
	}
	summary, body, ok := Parse(text)
	if !ok || body != "stdout:\n你好\n\n\nstderr:\nwarning\n\n\npty:\nterminal\n" || strings.Contains(text, "chunk_base64") {
		t.Fatalf("decoded view: %s", text)
	}
	if summary.ExitCode == nil || *summary.ExitCode != 0 || summary.OutputComplete == nil || !*summary.OutputComplete || summary.SandboxDenied == nil || *summary.SandboxDenied || summary.TimedOut == nil || *summary.TimedOut {
		t.Fatal("lost execution metadata", summary)
	}
}

func TestUnknownFailureDoesNotInventExitCode(t *testing.T) {
	wire := shell(chunk(1, "stderr", []byte("partial output")))
	delete(wire, "exit_code")
	wire["status"] = "unknown"
	wire["reason_code"] = "timeout"
	wire["timed_out"] = true
	wire["sandbox_denied"] = true
	wire["output_complete"] = false
	text, _, err := Render("executor", "shell", wireJSON(t, wire), 4096)
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := Parse(text)
	if s.ExitCode != nil || strings.Contains(text, "exit_code") || s.OutputComplete == nil || *s.OutputComplete || s.CommandStatus() != "unknown (timeout) (timed out) (sandbox denied) (output incomplete)" {
		t.Fatal(text, s.CommandStatus())
	}
}

func TestShellRejectsCorruptTransportAndLeavesUserBase64TextAlone(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(w map[string]any) {
			w["chunks"] = []any{map[string]any{"sequence": 1, "stream": "stdout", "chunk_base64": "!!!!"}}
		},
		func(w map[string]any) {
			w["chunks"] = []any{chunk(1, "stdout", []byte("a")), chunk(1, "stdout", []byte("b"))}
		},
		func(w map[string]any) { w["chunks"] = []any{chunk(1, "unknown", []byte("a"))} },
		func(w map[string]any) { delete(w, "output_complete") },
	} {
		wire := shell(chunk(1, "stdout", []byte("ok")))
		change(wire)
		if _, _, err := Render("executor", "shell", wireJSON(t, wire), 4096); err == nil {
			t.Fatal("accepted corrupt transport", wire)
		}
	}
	text, _, err := Render("executor", "shell", wireJSON(t, shell(chunk(1, "stdout", []byte("aGVsbG8=")))), 4096)
	if err != nil || !strings.Contains(text, "stdout:\naGVsbG8=") {
		t.Fatal("decoded user's own text", err, text)
	}
	if _, known, err := Render("custom", "shell", []byte("not JSON"), 100); known || err != nil {
		t.Fatal("interpreted unrelated namespace")
	}
}

func TestBinaryAndTextFileViewsAreExplicit(t *testing.T) {
	for _, content := range [][]byte{[]byte("文档\n\"text\""), bytes.Repeat([]byte{0, 255}, 1024)} {
		wire := map[string]any{"status": "succeeded", "path": "file", "offset": 10, "requested_bytes": len(content), "bytes_read": len(content), "eof": true, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}
		text, _, err := Render("executor", "read_file", wireJSON(t, wire), 4096)
		if err != nil {
			t.Fatal(err)
		}
		s, body, ok := Parse(text)
		if !ok || *s.BytesRead != uint64(len(content)) || *s.Offset != 10 || !*s.EOF {
			t.Fatal("lost file metadata", text)
		}
		if content[0] == 0 {
			if !s.NonText || s.TextBytesShown != nil || len(body) > 250 || !strings.Contains(body, "2048 bytes") {
				t.Fatal(text)
			}
		} else if body != string(content) || *s.TextBytesShown != uint64(len(content)) {
			t.Fatal(text)
		}
		wire["bytes_read"] = 1
		if _, _, err := Render("executor", "read_file", wireJSON(t, wire), 4096); err == nil {
			t.Fatal("accepted wrong file byte count")
		}
	}
}

func TestTextBudgetAccountsForEscapingAndReplayIsIdempotent(t *testing.T) {
	content := []byte(strings.Repeat("中文\n\"\\\x1b", 1000))
	wire := map[string]any{"status": "succeeded", "path": "file", "offset": 0, "requested_bytes": len(content), "bytes_read": len(content), "eof": true, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}
	text, _, err := Render("executor", "read_file", wireJSON(t, wire), 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{500, 400, 350} {
		text = Bound(text, limit)
		s, body, ok := Parse(text)
		encoded, _ := json.Marshal(text)
		if !ok || !s.DisplayTruncated || !utf8.ValidString(text) || len(encoded) > limit {
			t.Fatal("invalid bounded view", limit, text)
		}
		if s.TextBytesShown == nil || !strings.HasPrefix(body, string(content[:*s.TextBytesShown])) {
			t.Fatal("wrong visible source offset", text)
		}
		if Bound(text, limit) != text || Historical("executor.read_file", text, limit) != text {
			t.Fatal("replay changed rendered output")
		}
	}
}

func TestIncompleteUTF8FileBlockKeepsReadablePrefixAndNextOffset(t *testing.T) {
	content := append([]byte("中文\n"), []byte("后")[:2]...)
	wire := map[string]any{"status": "succeeded", "path": "file", "offset": 20, "requested_bytes": len(content), "bytes_read": len(content), "eof": false, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}
	text, _, err := Render("executor", "read_file", wireJSON(t, wire), 4096)
	if err != nil {
		t.Fatal(err)
	}
	s, body, ok := Parse(text)
	if !ok || s.NonText || s.PartialUTF8Bytes != 2 || !s.DisplayTruncated || *s.TextBytesShown != uint64(len("中文\n")) || !strings.HasPrefix(body, "中文\n") || strings.Contains(body, "�") {
		t.Fatal(text)
	}
	if Historical("executor.read_file", text, 4096) != text {
		t.Fatal("partial block replay changed its offset")
	}
	wire["eof"] = true
	complete, _, err := Render("executor", "read_file", wireJSON(t, wire), 4096)
	if err != nil {
		t.Fatal(err)
	}
	full, _, _ := Parse(complete)
	if !full.NonText || full.PartialUTF8Bytes != 0 {
		t.Fatal("silently repaired invalid complete file", complete)
	}
}

func TestPTYControlsDoNotHideReadableText(t *testing.T) {
	content := "\x1b]8;;https://example.invalid/\a中文链接\x1b]8;;\a\r进度\b\n"
	text, _, err := Render("executor", "shell", wireJSON(t, shell(chunk(1, "pty", []byte(content)))), 4096)
	if err != nil || !strings.Contains(text, content) || strings.Contains(text, "Non-text output") {
		t.Fatal("PTY text misclassified", err, text)
	}
}
