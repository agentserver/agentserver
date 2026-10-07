package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
)

type runtimeTransport func(*http.Request) (*http.Response, error)

func (f runtimeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, kind, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRuntimeClientHandshakeAndFencedStream(t *testing.T) {
	p, _, _, create := fixture(t)
	_ = p
	req := processRequest(create)
	boot := strings.Repeat("a", 64)
	e := Endpoint{Origin: "https://sandbox.test:8443", PodUID: "pod-1", BootID: boot}
	binding := k8sruntime.Binding{Identity: k8sruntime.Identity{PodUID: e.PodUID, BootID: boot}, Session: operationIdentity(req.Operation, req.Target.EnvironmentID).Session, Ref: runtimeRef(req.Target)}
	calls := 0
	client, err := NewHTTPRuntimeClient(&http.Client{Transport: runtimeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get(k8sruntime.PodHeader) != e.PodUID || r.Header.Get(k8sruntime.BootHeader) != boot {
			t.Fatal("missing incarnation fence")
		}
		if r.GetBody != nil {
			t.Fatal("runtime request may be replayed")
		}
		switch r.URL.Path {
		case k8sruntime.IdentityPath:
			raw, _ := json.Marshal(binding.Identity)
			return response(200, "application/json", string(raw)), nil
		case k8sruntime.BindPath:
			var got k8sruntime.Binding
			if json.NewDecoder(r.Body).Decode(&got) != nil || got != binding {
				t.Fatal("binding changed")
			}
			raw, _ := json.Marshal(got)
			return response(200, "application/json", string(raw)), nil
		default:
			var got sandboxcontract.RunCommandRequest
			if json.NewDecoder(r.Body).Decode(&got) != nil || got.Ref != binding.Ref || got.Identity != operationIdentity(req.Operation, req.Target.EnvironmentID) {
				t.Fatal("operation authority changed")
			}
			var raw strings.Builder
			for _, f := range []sandboxcontract.OperationFrame{
				{Type: sandboxcontract.OperationFrameAcknowledgement, Acknowledgement: &executionbackend.Acknowledgement{AcceptedAt: time.Now()}},
				{Type: sandboxcontract.OperationFrameEvent, Event: &executionbackend.Event{Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("version")}},
				{Type: sandboxcontract.OperationFrameTerminal, Terminal: &executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: time.Now()}},
			} {
				f.Profile = sandboxcontract.ProfileV1
				f.Identity = got.Identity
				f.Ref = got.Ref
				json.NewEncoder(&raw).Encode(f)
			}
			return response(200, "application/x-ndjson", raw.String()), nil
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := client.Identity(t.Context(), e); err != nil || got != boot {
		t.Fatalf("identity %q %v", got, err)
	}
	if err := client.Bind(t.Context(), e, binding); err != nil {
		t.Fatal(err)
	}
	ex, err := client.StartProcess(t.Context(), e, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ex.AwaitAcknowledgement(t.Context()); err != nil {
		t.Fatal(err)
	}
	if event, err := ex.NextEvent(t.Context()); err != nil || string(event.Data) != "version" {
		t.Fatalf("event %+v %v", event, err)
	}
	if result, err := ex.AwaitTerminal(t.Context()); err != nil || result.Status != executionbackend.TerminalSucceeded {
		t.Fatalf("terminal %+v %v", result, err)
	}
	if calls != 3 {
		t.Fatalf("unexpected request count: %d", calls)
	}
}

func TestRuntimeClientNeverRetriesAmbiguousStart(t *testing.T) {
	_, _, _, create := fixture(t)
	req := processRequest(create)
	calls := 0
	c, _ := NewHTTPRuntimeClient(&http.Client{Transport: runtimeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection reset SECRET")
	})})
	_, err := c.StartProcess(t.Context(), Endpoint{Origin: "https://sandbox.test", PodUID: "pod", BootID: strings.Repeat("a", 64)}, req)
	if executionbackend.OutcomeOf(err) != executionbackend.OutcomeUnknown || calls != 1 || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("retry or leak: %d %v", calls, err)
	}
}

func TestProviderPinsBootIdentityBeforeKubernetesStatusChanges(t *testing.T) {
	p, _, r, create := fixture(t)
	state, err := p.CreateSandbox(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	controllerObjects(t, p, create)
	if _, err := p.GetSandbox(t.Context(), state.SessionRef); err != nil {
		t.Fatal(err)
	}
	r.boot = strings.Repeat("b", 64)
	_, err = p.StartProcess(context.Background(), sandboxgateway.StartProcessProviderRequest{SessionRef: state.SessionRef, Request: processRequest(create)})
	if !executionbackend.ProvesNotSent(err) || r.calls != 0 {
		t.Fatal("dispatched to changed runtime boot with stale Pod status")
	}
}
