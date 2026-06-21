package ccappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWSTokenClient_GetOrCreate_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	token, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if token != "deadbeef" {
		t.Errorf("expected token 'deadbeef', got %q", token)
	}
}

func TestWSTokenClient_GetOrCreate_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	_, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code 401: %v", err)
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error should mention body: %v", err)
	}
}

func TestWSTokenClient_GetOrCreate_500Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	_, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code 500: %v", err)
	}
}

func TestWSTokenClient_GetOrCreate_EmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": ""})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	_, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
	if !strings.Contains(err.Error(), "empty") && !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention empty token: %v", err)
	}
}

func TestWSTokenClient_GetOrCreate_EmptyWorkspaceID(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	_, err := client.GetOrCreate(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty workspaceID, got nil")
	}
	if requestCount != 0 {
		t.Errorf("expected no HTTP requests for empty workspaceID, got %d", requestCount)
	}
}

func TestWSTokenClient_GetOrCreate_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetOrCreate(ctx, "test-workspace")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled: %v", err)
	}
}

func TestWSTokenClient_XInternalSecretHeader(t *testing.T) {
	var capturedSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSecret = r.Header.Get("X-Internal-Secret")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "test-secret")
	_, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if capturedSecret != "test-secret" {
		t.Errorf("expected X-Internal-Secret 'test-secret', got %q", capturedSecret)
	}
}

func TestWSTokenClient_NoSecretWhenEmpty(t *testing.T) {
	var capturedSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSecret = r.Header.Get("X-Internal-Secret")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "")
	_, err := client.GetOrCreate(context.Background(), "test-workspace")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if capturedSecret != "" {
		t.Errorf("expected no X-Internal-Secret header when empty, got %q", capturedSecret)
	}
}

func TestWSTokenClient_RequestPayload(t *testing.T) {
	var capturedBody struct {
		WorkspaceID string `json:"workspace_id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "deadbeef"})
	}))
	defer srv.Close()

	client := NewWSTokenClient(srv.URL, "secret")
	_, err := client.GetOrCreate(context.Background(), "my-workspace")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if capturedBody.WorkspaceID != "my-workspace" {
		t.Errorf("expected workspace_id 'my-workspace', got %q", capturedBody.WorkspaceID)
	}
}
