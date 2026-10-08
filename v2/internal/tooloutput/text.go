// Package tooloutput converts the executor's byte-oriented transport results
// to model-readable text. It does not execute tools or change transport schemas.
package tooloutput

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ShellFormat       = "agentserver.shell.text/v1"
	FileFormat        = "agentserver.read_file.text/v1"
	maxTransportBytes = 2 * 1024 * 1024
)

// Summary is a small first-line JSON header; the following body is literal
// readable text, not JSON-escaped output or encoded bytes. This also lets the
// server construct command cards without duplicating raw output in context.
type Summary struct {
	Format           string  `json:"format"`
	Status           string  `json:"status"`
	ProcessID        string  `json:"process_id,omitempty"`
	ReasonCode       string  `json:"reason_code,omitempty"`
	DispatchOutcome  string  `json:"dispatch_outcome,omitempty"`
	ExitCode         *int32  `json:"exit_code,omitempty"`
	SandboxDenied    *bool   `json:"sandbox_denied,omitempty"`
	TimedOut         *bool   `json:"timed_out,omitempty"`
	OutputComplete   *bool   `json:"output_complete,omitempty"`
	NextSequence     uint64  `json:"next_sequence,omitempty"`
	Path             string  `json:"path,omitempty"`
	Offset           *uint64 `json:"offset,omitempty"`
	RequestedBytes   uint64  `json:"requested_bytes,omitempty"`
	BytesRead        *uint64 `json:"bytes_read,omitempty"`
	EOF              *bool   `json:"eof,omitempty"`
	DisplayTruncated bool    `json:"display_truncated,omitempty"`
	NonText          bool    `json:"non_text,omitempty"`
	TextBytesShown   *uint64 `json:"text_bytes_shown,omitempty"`
	PartialUTF8Bytes int     `json:"trailing_partial_utf8_bytes,omitempty"`
}

type shellWire struct {
	ProcessID       string `json:"process_id"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code"`
	DispatchOutcome string `json:"dispatch_outcome"`
	ExitCode        *int32 `json:"exit_code"`
	SandboxDenied   bool   `json:"sandbox_denied"`
	TimedOut        bool   `json:"timed_out"`
	OutputComplete  *bool  `json:"output_complete"`
	NextSequence    uint64 `json:"next_sequence"`
	Chunks          *[]struct {
		Sequence uint64 `json:"sequence"`
		Stream   string `json:"stream"`
		Chunk    string `json:"chunk_base64"`
	} `json:"chunks"`
}

// Render only interprets the two named executor contracts. Unknown tools and
// namespaces remain opaque, even if their result happens to contain base64.
func Render(namespace, tool string, raw []byte, limit int) (string, bool, error) {
	if namespace != "executor" || (tool != "shell" && tool != "read_file") {
		return "", false, nil
	}
	if len(raw) > maxTransportBytes {
		return "", true, errors.New("tool output transport exceeds bound")
	}
	var summary Summary
	var body string
	var err error
	if tool == "shell" {
		summary, body, err = decodeShell(raw)
	} else {
		summary, body, err = decodeFile(raw)
	}
	if err != nil {
		return "", true, err
	}
	text, err := render(summary, body, limit)
	return text, true, err
}

func decodeShell(raw []byte) (Summary, string, error) {
	var wire shellWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Summary{}, "", errors.New("invalid shell result JSON")
	}
	if !validStatus(wire.Status) || wire.Chunks == nil || wire.OutputComplete == nil {
		return Summary{}, "", errors.New("missing shell result fields")
	}
	if wire.DispatchOutcome != "" && (wire.DispatchOutcome != "not_sent" || wire.Status != "failed" || wire.ExitCode != nil || !*wire.OutputComplete || len(*wire.Chunks) != 0) {
		return Summary{}, "", errors.New("inconsistent shell pre-dispatch failure")
	}
	streams := map[string][]byte{}
	var order []string
	var previous uint64
	var total int
	for _, chunk := range *wire.Chunks {
		if chunk.Sequence == 0 || chunk.Sequence <= previous {
			return Summary{}, "", errors.New("shell output sequence is not increasing")
		}
		if chunk.Stream != "stdout" && chunk.Stream != "stderr" && chunk.Stream != "pty" {
			return Summary{}, "", errors.New("unknown shell output stream")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(chunk.Chunk)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != chunk.Chunk {
			return Summary{}, "", errors.New("invalid shell output encoding")
		}
		total += len(decoded)
		if total > maxTransportBytes {
			return Summary{}, "", errors.New("decoded shell output exceeds bound")
		}
		if _, ok := streams[chunk.Stream]; !ok {
			order = append(order, chunk.Stream)
		}
		streams[chunk.Stream] = append(streams[chunk.Stream], decoded...)
		previous = chunk.Sequence
	}
	if wire.NextSequence != 0 && wire.NextSequence <= previous {
		return Summary{}, "", errors.New("invalid next shell output sequence")
	}
	var body strings.Builder
	for _, stream := range order {
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(stream + ":\n")
		data, partial := textPrefix(streams[stream], !*wire.OutputComplete)
		body.WriteString(readableBytes(data))
		if partial > 0 {
			body.WriteString(fmt.Sprintf("\n[Incomplete UTF-8 suffix: %d bytes, hex: %s.]", partial, hex.EncodeToString(streams[stream][len(data):])))
		}
	}
	if len(order) == 0 {
		if wire.DispatchOutcome == "not_sent" {
			body.WriteString("Command was not started: managed process environment setup failed before dispatch. ")
			switch wire.ReasonCode {
			case "credential_unauthorized", "forbidden":
				body.WriteString("Credential authorization was rejected; check the environment scope and workspace access. This does not establish that the saved credentials are invalid.")
			case "credential_not_configured", "bytecloud_aksk_required":
				body.WriteString("Configure a default ByteCloud AK/SK credential in this workspace.")
			default:
				body.WriteString("Check the executor/Core service logs for the failure reason.")
			}
		} else {
			body.WriteString("(no output)")
		}
	}
	return Summary{Format: ShellFormat, Status: wire.Status, ProcessID: wire.ProcessID, ReasonCode: wire.ReasonCode, DispatchOutcome: wire.DispatchOutcome, ExitCode: wire.ExitCode, SandboxDenied: &wire.SandboxDenied, TimedOut: &wire.TimedOut, OutputComplete: wire.OutputComplete, NextSequence: wire.NextSequence}, body.String(), nil
}

func decodeFile(raw []byte) (Summary, string, error) {
	var wire struct {
		Status         string  `json:"status"`
		Path           string  `json:"path"`
		Offset         *uint64 `json:"offset"`
		RequestedBytes uint64  `json:"requested_bytes"`
		BytesRead      *uint64 `json:"bytes_read"`
		EOF            *bool   `json:"eof"`
		Encoding       string  `json:"encoding"`
		Content        *string `json:"content"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Summary{}, "", errors.New("invalid read_file result JSON")
	}
	if !validStatus(wire.Status) || wire.Path == "" || wire.Offset == nil || wire.BytesRead == nil || wire.EOF == nil || wire.Content == nil {
		return Summary{}, "", errors.New("missing read_file result fields")
	}
	var content []byte
	switch wire.Encoding {
	case "utf-8":
		content = []byte(*wire.Content)
	case "base64":
		var err error
		content, err = base64.StdEncoding.Strict().DecodeString(*wire.Content)
		if err != nil || base64.StdEncoding.EncodeToString(content) != *wire.Content {
			return Summary{}, "", errors.New("invalid read_file content encoding")
		}
	default:
		return Summary{}, "", errors.New("unknown read_file encoding")
	}
	if len(content) > maxTransportBytes || uint64(len(content)) != *wire.BytesRead {
		return Summary{}, "", errors.New("read_file byte count mismatch")
	}
	prefix, partial := textPrefix(content, !*wire.EOF)
	return Summary{Format: FileFormat, Status: wire.Status, Path: wire.Path, Offset: wire.Offset, RequestedBytes: wire.RequestedBytes, BytesRead: wire.BytesRead, EOF: wire.EOF, NonText: !isText(prefix), PartialUTF8Bytes: partial, DisplayTruncated: partial > 0}, readableBytes(prefix), nil
}

// A bounded file block or incomplete process output can end within a UTF-8
// code point. Preserve its valid prefix and explicitly account for the tail;
// do not label an otherwise readable document as binary or insert U+FFFD.
func textPrefix(content []byte, allowIncomplete bool) ([]byte, int) {
	if isText(content) || !allowIncomplete {
		return content, 0
	}
	for tail := 1; tail <= 3 && tail <= len(content); tail++ {
		prefix, suffix := content[:len(content)-tail], content[len(content)-tail:]
		if !utf8.FullRune(suffix) && isText(prefix) {
			return prefix, tail
		}
	}
	return content, 0
}

func validStatus(status string) bool {
	return status == "succeeded" || status == "failed" || status == "unknown"
}

func readableBytes(content []byte) string {
	if isText(content) {
		return string(content)
	}
	preview := content
	if len(preview) > 32 {
		preview = preview[:32]
	}
	return fmt.Sprintf("[Non-text output: %d bytes; first %d bytes (hex): %s. Raw bytes are not included in this text view.]", len(content), len(preview), hex.EncodeToString(preview))
}

func isText(content []byte) bool {
	if !utf8.Valid(content) {
		return false
	}
	for _, r := range string(content) {
		// Common PTY controls (including OSC's BEL terminator) do not turn
		// otherwise valid terminal text into a binary file. NUL and unrelated
		// controls remain non-text and get a bounded byte preview.
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\a' && r != '\b' && r != '\v' && r != '\f' && r != 0x1b {
			return false
		}
	}
	return true
}

func render(summary Summary, body string, limit int) (string, error) {
	const truncationSuffix = "\n[Output truncated in this text view.]"
	if summary.DisplayTruncated {
		body = strings.TrimSuffix(body, truncationSuffix)
	}
	// Account for JSON escaping too: a readable control-heavy string must not
	// overflow the existing app-server/control frame when encoded as inputText.
	candidate := func(size int) (string, bool) {
		for size > 0 && size < len(body) && !utf8.RuneStart(body[size]) {
			size--
		}
		view := summary
		suffix := ""
		if size < len(body) {
			view.DisplayTruncated = true
		}
		if view.DisplayTruncated {
			suffix = truncationSuffix
		}
		if view.Format == FileFormat && !view.NonText {
			n := uint64(size)
			view.TextBytesShown = &n
		}
		header, _ := json.Marshal(view)
		text := string(header) + "\n\n" + body[:size] + suffix
		encoded, _ := json.Marshal(text)
		return text, len(encoded) <= limit
	}
	if text, ok := candidate(len(body)); ok {
		return text, nil
	}
	best, ok := candidate(0)
	if !ok {
		return "", errors.New("tool output metadata exceeds text bound")
	}
	low, high := 0, len(body)-1
	for low <= high {
		mid := low + (high-low)/2
		text, fits := candidate(mid)
		if fits {
			best = text
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best, nil
}

// Parse recognizes only the explicit worker text profile. It never guesses
// whether ordinary command output is itself base64 or JSON.
func Parse(text string) (Summary, string, bool) {
	header, body, ok := strings.Cut(text, "\n\n")
	if !ok || len(header) > 16*1024 {
		return Summary{}, "", false
	}
	var summary Summary
	if json.Unmarshal([]byte(header), &summary) != nil || !validStatus(summary.Status) || (summary.Format != ShellFormat && summary.Format != FileFormat) {
		return Summary{}, "", false
	}
	return summary, body, true
}

func Bound(text string, limit int) string {
	if summary, body, ok := Parse(text); ok {
		if bounded, err := render(summary, body, limit); err == nil {
			return bounded
		}
	}
	return text
}

// Historical renders complete old transport JSON at read time without changing
// persisted events or cursor counts. Already-omitted/truncated old data cannot
// be recovered here; leave those explicit omission markers unchanged.
func Historical(qualifiedTool, text string, limit int) string {
	namespace, tool, ok := strings.Cut(qualifiedTool, ".")
	if !ok || namespace != "executor" {
		return text
	}
	if summary, _, ok := Parse(text); ok && ((tool == "shell" && summary.Format == ShellFormat) || (tool == "read_file" && summary.Format == FileFormat)) {
		return Bound(text, limit)
	}
	raw := bytes.TrimSpace([]byte(text))
	if len(raw) == 0 || raw[0] != '{' {
		return text
	}
	if rendered, known, err := Render(namespace, tool, raw, limit); known && err == nil {
		return rendered
	}
	return text
}

func (summary Summary) CommandStatus() string {
	status := summary.Status
	if summary.ReasonCode != "" {
		status += " (" + summary.ReasonCode + ")"
	}
	if summary.ExitCode != nil {
		status += fmt.Sprintf(" (exit %d)", *summary.ExitCode)
	}
	if summary.TimedOut != nil && *summary.TimedOut {
		status += " (timed out)"
	}
	if summary.SandboxDenied != nil && *summary.SandboxDenied {
		status += " (sandbox denied)"
	}
	if summary.OutputComplete != nil && !*summary.OutputComplete {
		status += " (output incomplete)"
	}
	if summary.DisplayTruncated {
		status += " (text truncated)"
	}
	return status
}
