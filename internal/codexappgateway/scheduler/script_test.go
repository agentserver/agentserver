// internal/codexappgateway/scheduler/script_test.go
package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunPreScript_Wake(t *testing.T) {
	wake, data, err := RunPreScript(context.Background(),
		`echo '{"wakeAgent":true,"data":{"x":1}}'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !wake {
		t.Fatal("want wake=true")
	}
	if string(data) != `{"x":1}` {
		t.Fatalf("data=%s", string(data))
	}
}

func TestRunPreScript_Skip(t *testing.T) {
	wake, _, err := RunPreScript(context.Background(),
		`echo '{"wakeAgent":false,"data":null}'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wake {
		t.Fatal("want wake=false")
	}
}

func TestRunPreScript_BadJSON(t *testing.T) {
	_, _, err := RunPreScript(context.Background(), `echo notjson`, nil)
	if err == nil {
		t.Fatal("want error on non-JSON output")
	}
	if !strings.Contains(err.Error(), "must print JSON") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunPreScript_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := RunPreScript(ctx, `sleep 2`, nil)
	if err == nil {
		t.Fatal("want timeout error")
	}
}
