package adapter

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

func TestRuntimeErrorPreservesSafeProviderCode(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusConflict, Body: io.NopCloser(strings.NewReader(`{"code":"repository_busy","message":"repository_busy","outcome":"rejected"}`))}
	document := readRuntimeError(response)
	if !document.IsBusy() {
		t.Fatal("repository_busy was not classified as retryable")
	}
	err := runtimeDispatchErrorFromDocument(http.StatusConflict, document)
	dispatch, ok := err.(*executionbackend.DispatchError)
	if !ok || dispatch.Code != "runtime_repository_busy" || dispatch.ProviderCode != "repository_busy" || dispatch.Outcome != executionbackend.OutcomeRejected {
		t.Fatalf("dispatch error = %#v", err)
	}
}

func TestRuntimeErrorDoesNotFenceOnDuplicateOrBootConflict(t *testing.T) {
	for _, code := range []string{"duplicate_or_fenced", "runtime_replaced", "binding_conflict"} {
		response := &http.Response{StatusCode: http.StatusConflict, Body: io.NopCloser(strings.NewReader(`{"code":"` + code + `","message":"` + code + `","outcome":"rejected"}`))}
		document := readRuntimeError(response)
		dispatch, ok := runtimeDispatchErrorFromDocument(http.StatusConflict, document).(*executionbackend.DispatchError)
		if !ok || dispatch.Outcome != executionbackend.OutcomeUnknown || dispatch.ProviderCode != code {
			t.Fatalf("%s dispatch = %#v", code, dispatch)
		}
	}
}
