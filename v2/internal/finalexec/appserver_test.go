package finalexec

import (
	"reflect"
	"strings"
	"testing"
)

func TestAppServerConfigUsesFixedStockCommandAndExplicitEnvironment(t *testing.T) {
	arguments := AppServerArguments("/opt/agentserver/bin/codex", "/run/agentserver/empty", 65532, 65533)
	environment := []string{"CODEX_HOME=/run/agentserver/codex", "AGENTSERVER_LLM_CAPABILITY=sensitive"}
	config, err := appServerConfig(arguments, environment)
	if err != nil {
		t.Fatal(err)
	}
	if config.Program != "/opt/agentserver/bin/codex" || config.Directory != "/run/agentserver/empty" ||
		config.ExpectedUID != 65532 || config.ExpectedGID != 65533 {
		t.Fatalf("app-server config = %+v", config)
	}
	if got, want := config.Arguments, []string{"app-server", "--listen", "stdio://", "--strict-config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("app-server arguments = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(config.Environment, environment) || &config.Environment[0] == &environment[0] {
		t.Fatalf("app-server environment was not copied: got=%v", config.Environment)
	}
}

func TestAppServerConfigRejectsOpenEndedOrNonCanonicalLauncherArguments(t *testing.T) {
	base := AppServerArguments("/opt/codex", "/empty", 65532, 65532)
	tests := []struct {
		name   string
		mutate func([]string) []string
		want   string
	}{
		{name: "missing", mutate: func(args []string) []string { return args[:3] }, want: "exactly four"},
		{name: "extra target argument", mutate: func(args []string) []string { return append(args, "exec") }, want: "exactly four"},
		{name: "wrong order", mutate: func(args []string) []string { args[0], args[1] = args[1], args[0]; return args }, want: "program"},
		{name: "zero uid", mutate: func(args []string) []string { args[2] = "--expected-uid=0"; return args }, want: "unprivileged"},
		{name: "leading zero", mutate: func(args []string) []string { args[3] = "--expected-gid=065532"; return args }, want: "canonical"},
		{name: "unknown flag", mutate: func(args []string) []string { args[3] = "--gid=65532"; return args }, want: "gid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := append([]string(nil), base...)
			_, err := appServerConfig(test.mutate(arguments), []string{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("appServerConfig() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := appServerConfig(base, nil); err == nil || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("nil environment error = %v", err)
	}
}
