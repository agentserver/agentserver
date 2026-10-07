package executorgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func TestKubernetesBackendUsesSameFencedStreamingProtocol(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cross_provider_frame"}[corrupt], func(t *testing.T) {
			calls := 0
			backend, err := NewManagedBackend(executionbackend.KindKubernetes, "https://sandbox-gateway.internal", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var command sandboxcontract.RunCommandRequest
				if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
					t.Fatal(err)
				}
				if command.Ref.BackendKind != executionbackend.KindKubernetes {
					t.Fatal("Kubernetes request sent as TAE")
				}
				ack := executionbackend.Acknowledgement{AcceptedAt: time.Now()}
				event := executionbackend.Event{Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("hello")}
				terminal := executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: time.Now()}
				if corrupt {
					command.Ref.BackendKind = executionbackend.KindTAE
				}
				return operationHTTPResponse(encodeOperationFrames(t,
					sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameAcknowledgement, Identity: command.Identity, Ref: command.Ref, Acknowledgement: &ack},
					sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameEvent, Identity: command.Identity, Ref: command.Ref, Event: &event},
					sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameTerminal, Identity: command.Identity, Ref: command.Ref, Terminal: &terminal})), nil
			})}, &recordingSandboxTokenSource{token: "test-backend-capability-token"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := validTAEStartRequest()
			req.Target.Kind = executionbackend.KindKubernetes
			exchange, err := backend.StartProcess(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			_, err = exchange.AwaitAcknowledgement(t.Context())
			if corrupt {
				if executionbackend.OutcomeOf(err) != executionbackend.OutcomeUnknown {
					t.Fatalf("foreign stream: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			event, err := exchange.NextEvent(t.Context())
			if err != nil || string(event.Data) != "hello" {
				t.Fatalf("stream: %+v %v", event, err)
			}
			if _, err := exchange.NextEvent(t.Context()); err != io.EOF {
				t.Fatalf("missing terminal: %v", err)
			}
			terminal, err := exchange.AwaitTerminal(t.Context())
			if err != nil || !terminal.OutputComplete {
				t.Fatalf("terminal: %+v %v", terminal, err)
			}
			req.Target.Kind = executionbackend.KindTAE
			if _, err := backend.StartProcess(t.Context(), req); !executionbackend.ProvesNotSent(err) || calls != 1 {
				t.Fatal("Kubernetes backend accepted TAE target")
			}
		})
	}
}
