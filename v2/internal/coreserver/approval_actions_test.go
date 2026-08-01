package coreserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestInternalApprovalActionHandlerSeparatesObservationFromCommands(t *testing.T) {
	commands := &recordingApprovalRoute{status: http.StatusNoContent}
	observation := &recordingApprovalRoute{status: http.StatusAccepted}
	router, err := NewInternalApprovalActionHandler(commands, observation)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(corecontract.ApprovalActionRoutePattern, router)

	approvalID := "40000000-0000-4000-8000-000000000071"
	tests := []struct {
		name             string
		path             string
		wantStatus       int
		wantCommands     int
		wantObservations int
	}{
		{name: "consume", path: corecontract.ConsumeApprovalPath(approvalID), wantStatus: http.StatusNoContent, wantCommands: 1},
		{name: "cancel", path: corecontract.CancelApprovalPath(approvalID), wantStatus: http.StatusNoContent, wantCommands: 2},
		{name: "expire", path: corecontract.ExpireApprovalPath(approvalID), wantStatus: http.StatusNoContent, wantCommands: 3},
		{name: "observe", path: corecontract.ObserveApprovalPath(approvalID), wantStatus: http.StatusAccepted, wantCommands: 3, wantObservations: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus || commands.calls != test.wantCommands || observation.calls != test.wantObservations {
				t.Fatalf("response=%d command calls=%d observation calls=%d", response.Code, commands.calls, observation.calls)
			}
		})
	}
}

type recordingApprovalRoute struct {
	status int
	calls  int
}

func (route *recordingApprovalRoute) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	route.calls++
	response.WriteHeader(route.status)
}
