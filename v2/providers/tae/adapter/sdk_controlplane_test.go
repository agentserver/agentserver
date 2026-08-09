package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"code.byted.org/inf/bytedai-go/sandbox"
)

func TestSDKControlPlaneInjectsApplicationJWTIntoEveryControlRequest(t *testing.T) {
	requestCount := 0
	mux := http.NewServeMux()
	assertIdentity := func(request *http.Request) {
		t.Helper()
		requestCount++
		if got := request.Header.Get("X-Jwt-Token"); got != "header.payload.signature" {
			t.Fatalf("control-plane X-Jwt-Token = %q", got)
		}
		if request.Header.Get("X-Zti-Token") != "" {
			t.Fatal("control-plane request also carried ZTI identity")
		}
	}
	mux.HandleFunc("/api/v1/sandboxes/psm.agentserver.tae", func(response http.ResponseWriter, request *http.Request) {
		assertIdentity(request)
		_ = json.NewEncoder(response).Encode(sandbox.SandboxMetaResponse{
			Code: 0,
			Data: &sandbox.SandboxMeta{SandboxID: "sandbox-1", SandboxType: sandbox.SandboxTypeTerminal,
				Name: "agentserver", Psm: "psm.agentserver.tae"},
		})
	})
	mux.HandleFunc("/api/v1/sandboxes/sandbox-1/sessions/search", func(response http.ResponseWriter, request *http.Request) {
		assertIdentity(request)
		_ = json.NewEncoder(response).Encode(sandbox.SessionsSearchResponse{
			Code: 0, Data: &sandbox.SessionsSearchResponseData{Sessions: []*sandbox.SessionInfoResponseData{}, Total: 0},
		})
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	control, err := NewSGSDKControlPlane(context.Background(), SDKControlPlaneConfig{
		PSM: "psm.agentserver.tae", HTTPClient: server.Client(), Headers: staticJWTSource(),
		ControlPlaneURL: server.URL, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Search(t.Context(), SearchInput{Metadata: map[string]string{"probe": "never"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || len(result.Sessions) != 0 || requestCount < 2 {
		t.Fatalf("control-plane search = %+v, requests=%d", result, requestCount)
	}
}

func TestConvertSDKSearchResultCarriesTotalAndAllowsDeletedEmptyExpiry(t *testing.T) {
	metadata := map[string]string{"agentserver_sandbox_id": "sandbox-1"}
	result, err := convertSDKSearchResult(&sandbox.SessionsSearchResponseData{
		Total: 2,
		Sessions: []*sandbox.SessionInfoResponseData{
			{SessionID: "session-1", Status: "running", ExpiresAt: "2026-08-06T21:00:00Z", Image: testTAEImage, Metadata: metadata, SandboxdEnabled: true},
			{SessionID: "session-2", Status: "terminated", Deleted: true, Metadata: metadata},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Sessions) != 2 {
		t.Fatalf("search result = %+v", result)
	}
	if result.Sessions[0].ExpiresAt.IsZero() || !result.Sessions[1].Deleted || !result.Sessions[1].ExpiresAt.IsZero() {
		t.Fatalf("converted sessions = %+v", result.Sessions)
	}
	if !reflect.DeepEqual(result.Sessions[0].Metadata, metadata) {
		t.Fatalf("metadata was not copied exactly: %#v", result.Sessions[0].Metadata)
	}
	metadata["agentserver_sandbox_id"] = "mutated"
	if result.Sessions[0].Metadata["agentserver_sandbox_id"] != "sandbox-1" {
		t.Fatal("converted metadata aliases SDK response")
	}
}

func TestConvertSDKSearchResultRejectsUnprovableEnumeration(t *testing.T) {
	cases := []struct {
		name   string
		result *sandbox.SessionsSearchResponseData
	}{
		{name: "negative total", result: &sandbox.SessionsSearchResponseData{Total: -1}},
		{name: "short page", result: &sandbox.SessionsSearchResponseData{Total: 1, Sessions: []*sandbox.SessionInfoResponseData{
			{SessionID: "session-1", ExpiresAt: "2026-08-06T21:00:00Z"},
			{SessionID: "session-2", ExpiresAt: "2026-08-06T21:00:00Z"},
		}}},
		{name: "nil session", result: &sandbox.SessionsSearchResponseData{Total: 1, Sessions: []*sandbox.SessionInfoResponseData{nil}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := convertSDKSearchResult(testCase.result); err == nil {
				t.Fatal("invalid TAE search response was accepted")
			}
		})
	}
}

func TestConvertSDKSessionRejectsMalformedLiveExpiryButAcceptsDeletedEmptyExpiry(t *testing.T) {
	if _, err := convertSDKSessionInfo(&sandbox.SessionInfoResponseData{SessionID: "live", ExpiresAt: "not-a-time"}); err == nil {
		t.Fatal("live session with malformed expiry was accepted")
	}
	deleted, err := convertSDKSessionInfo(&sandbox.SessionInfoResponseData{SessionID: "deleted", Deleted: true})
	if err != nil || !deleted.Deleted || !deleted.ExpiresAt.IsZero() {
		t.Fatalf("deleted session conversion = %+v, %v", deleted, err)
	}
	full, err := convertSDKSession(&sandbox.Session{
		SessionID: "session-1", ExpiresAt: "2026-08-06T21:00:00+00:00",
		AdvancedInfo: &sandbox.AdvancedInfo{Status: "running", Image: testTAEImage, SandboxdEnabled: true},
	})
	if err != nil || !full.ExpiresAt.Equal(time.Date(2026, 8, 6, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("full session conversion = %+v, %v", full, err)
	}
}

func TestParseTAETimeIsStrictRFC3339(t *testing.T) {
	for _, value := range []string{"2026-08-06T21:00:00Z", "2026-08-06T21:00:00.123456789Z"} {
		if parsed, err := parseTAETime(value); err != nil || parsed.IsZero() {
			t.Fatalf("parseTAETime(%q) = %v, %v", value, parsed, err)
		}
	}
	for _, value := range []string{"", "2026-08-06 21:00:00Z", strings.Repeat("x", 64)} {
		if _, err := parseTAETime(value); err == nil {
			t.Fatalf("parseTAETime(%q) unexpectedly succeeded", value)
		}
	}
}
