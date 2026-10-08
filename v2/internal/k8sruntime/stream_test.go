package k8sruntime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

// Keep real HTTP/2 deadline behavior, but shorten the per-write bound so the
// regression does not need a 15-second pause on every test run.
type shortDeadlineWriter struct{ http.ResponseWriter }

func (w shortDeadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w shortDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		deadline = time.Now().Add(250 * time.Millisecond)
	}
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}

func TestStreamWriteDeadlineDoesNotExpireBetweenFrames(t *testing.T) {
	cases := []struct {
		name         string
		http2, short bool
		delay        string
	}{
		{"http1-short", false, true, "0.75"}, {"http2-short", true, true, "0.75"},
	}
	if os.Getenv("AGENTSERVER_RUN_RUNTIME_STREAM_TESTS") == "1" {
		cases = append(cases, struct {
			name         string
			http2, short bool
			delay        string
		}{"http2-production-20s", true, false, "20"})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			s := testServer(t)
			command := testCommand(s)
			command.TimeoutMillis = 30000
			command.Arguments = []string{"-c", "sleep " + test.delay + "; printf 'after quiet interval'"}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.short {
					w = shortDeadlineWriter{w}
				}
				s.run(w, r, command)
			}))
			server.EnableHTTP2 = test.http2
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 35 * time.Second
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if (resp.ProtoMajor == 2) != test.http2 {
				t.Fatalf("wrong test protocol: %s", resp.Proto)
			}
			decoder := json.NewDecoder(resp.Body)
			for i, want := range []sandboxcontract.OperationFrameType{sandboxcontract.OperationFrameAcknowledgement, sandboxcontract.OperationFrameEvent, sandboxcontract.OperationFrameTerminal} {
				var frame sandboxcontract.OperationFrame
				if err := decoder.Decode(&frame); err != nil {
					t.Fatalf("frame %d after idle interval: %v", i, err)
				}
				if frame.Type != want || frame.Validate() != nil {
					t.Fatalf("invalid frame: %+v", frame)
				}
				if frame.Terminal != nil && (frame.Terminal.Status != executionbackend.TerminalSucceeded || frame.Terminal.ExitCode == nil || *frame.Terminal.ExitCode != 0 || !frame.Terminal.OutputComplete) {
					t.Fatalf("quiet process failed: %+v", frame.Terminal)
				}
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("stream did not close normally: %v", err)
			}
		})
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	fail      bool
}

func (w *deadlineRecorder) SetWriteDeadline(d time.Time) error {
	w.deadlines = append(w.deadlines, d)
	return nil
}
func (w *deadlineRecorder) FlushError() error {
	if w.fail {
		return errors.New("blocked writer")
	}
	return nil
}

func TestStreamRetainsWriteBoundAndClearsItOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := testServer(t)
		command := testCommand(s)
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), fail: fail}
		err := newStream(w, command.Identity, command.Ref).ack("process-1")
		if (err != nil) != fail {
			t.Fatalf("flush failure lost: %v", err)
		}
		if len(w.deadlines) != 2 || w.deadlines[0].IsZero() || !w.deadlines[1].IsZero() {
			t.Fatalf("deadline did not bracket write/flush: %v", w.deadlines)
		}
	}
}
