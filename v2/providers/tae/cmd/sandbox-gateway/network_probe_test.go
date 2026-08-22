package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedruntime"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

func TestExecuteNetworkProbeCoversJWTControlDataAndCleanup(t *testing.T) {
	cli := []byte("fake pinned lark cli")
	skill := []byte("# fake pinned skill\n")
	files := testProbeFiles(cli, skill)
	control := &probeControl{}
	data := &probeData{files: files}
	refreshes := 0
	clients := &taeClients{
		refresh: func(context.Context) error { refreshes++; return nil },
		control: control, data: data, close: func() {},
	}
	config := testNetworkProbeConfig(files)
	report, err := executeNetworkProbe(t.Context(), config, clients)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.CleanupConfirmed || refreshes != config.connectivityAttempts || !control.deleted {
		t.Fatalf("report=%+v refreshes=%d control=%+v", report, refreshes, control)
	}
	checks := checksByName(report.Checks)
	if checks["jwt_force_refresh"].Attempts != config.connectivityAttempts ||
		checks["control_search_missing"].Attempts != config.connectivityAttempts ||
		checks["data_exec_terminal"].Succeeded != config.lifecycleAttempts ||
		checks["data_read_lark_cli"].BytesRead != int64(len(cli)) ||
		checks["data_read_lark_skill"].BytesRead != int64(len(skill)) ||
		checks["data_exec_bkectl_version"].Succeeded != config.lifecycleAttempts ||
		checks["data_read_bkectl_cli"].BytesRead != int64(len(files[probeBkectlCLIPath])) ||
		checks["control_cleanup"].Failed != 0 {
		t.Fatalf("checks = %+v", checks)
	}
	raw, err := taenetworkreport.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"access-key", "secret-key", "jwt-token"} {
		if bytes.Contains(bytes.ToLower(raw), []byte(forbidden)) {
			t.Fatalf("report contains forbidden material label %q: %s", forbidden, raw)
		}
	}
}

func TestExecuteNetworkProbeAcceptsTAEOmittedCommandAndProvesRuntimeThroughDataPlane(t *testing.T) {
	cli := []byte("fake pinned lark cli")
	skill := []byte("# fake pinned skill\n")
	files := testProbeFiles(cli, skill)
	control := &probeControl{omitCommand: true}
	data := &probeData{files: files}
	report, err := executeNetworkProbe(t.Context(), testNetworkProbeConfig(files), &taeClients{
		refresh: func(context.Context) error { return nil }, control: control, data: data, close: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks := checksByName(report.Checks)
	if !report.Passed || checks["control_wait_ready"].Failed != 0 || checks["data_exec_terminal"].Succeeded != 1 ||
		checks["data_exec_lark_version"].Succeeded != 1 || checks["data_exec_bkectl_version"].Succeeded != 1 {
		t.Fatalf("report=%+v checks=%+v", report, checks)
	}
}

func TestValidateBkectlVersionOutputUsesTheJSONContract(t *testing.T) {
	tests := []struct {
		name   string
		output string
		valid  bool
	}{
		{
			name:   "field order and whitespace do not matter",
			output: `{ "data": { "version": "` + testBkectlRevision + `", "build_time": "1970-01-01T00:00:00Z" }, "success": true, "error": null }`,
			valid:  true,
		},
		{name: "human output is rejected", output: "bkectl version " + testBkectlRevision},
		{name: "wrong version is rejected", output: `{"success":true,"data":{"version":"wrong","build_time":"1970-01-01T00:00:00Z"}}`},
		{name: "wrong build time is rejected", output: `{"success":true,"data":{"version":"` + testBkectlRevision + `","build_time":"now"}}`},
		{name: "failed response is rejected", output: `{"success":false,"data":{"version":"` + testBkectlRevision + `","build_time":"1970-01-01T00:00:00Z"}}`},
		{name: "trailing output is rejected", output: `{"success":true,"data":{"version":"` + testBkectlRevision + `","build_time":"1970-01-01T00:00:00Z"}} trailing`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBkectlVersionOutput(test.output, testBkectlRevision)
			if (err == nil) != test.valid {
				t.Fatalf("validateBkectlVersionOutput() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestExecuteNetworkProbeRecordsSanitizedFailureAndStillCleansUp(t *testing.T) {
	cli := []byte("fake pinned lark cli")
	skill := []byte("# fake pinned skill\n")
	files := testProbeFiles(cli, skill)
	control := &probeControl{}
	data := &probeData{files: files, startError: &adapter.RequestError{
		WroteRequest: true, Code: "request_timeout", Cause: errors.New("must not appear in report"),
	}}
	clients := &taeClients{refresh: func(context.Context) error { return nil }, control: control, data: data, close: func() {}}
	report, err := executeNetworkProbe(t.Context(), testNetworkProbeConfig(files), clients)
	if err != nil {
		t.Fatal(err)
	}
	check := checksByName(report.Checks)["data_exec_lark_version"]
	if report.Passed || !report.CleanupConfirmed || check.Failed != 1 || check.Errors["request_timeout"] != 1 || !control.deleted {
		t.Fatalf("failed report=%+v check=%+v", report, check)
	}
	raw, err := taenetworkreport.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("must not appear")) {
		t.Fatalf("provider error leaked into report: %s", raw)
	}
}

func TestProbeNetworkEmitsCanonicalFailureReportInsteadOfRuntimeDiagnostics(t *testing.T) {
	values := validNetworkProbeEnvironment()
	var output bytes.Buffer
	passed, err := probeNetworkWithFactory(t.Context(), func(name string) string { return values[name] }, &output,
		func(context.Context, providerConfig, string) (*taeClients, error) {
			control := &probeControl{createError: &adapter.RequestError{Code: "provider_unavailable"}}
			return &taeClients{
				refresh: func(context.Context) error { return nil }, control: control,
				data: &probeData{}, close: func() {},
			}, nil
		})
	if err != nil || passed {
		t.Fatalf("probeNetworkWithFactory() passed=%v error=%v", passed, err)
	}
	report, err := taenetworkreport.Parse(output.Bytes())
	if err != nil {
		t.Fatalf("output is not a canonical report: %v\n%s", err, output.Bytes())
	}
	if report.Passed || checksByName(report.Checks)["control_create"].Errors["provider_unavailable"] != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestLoadNetworkProbeConfigFailsClosedOnUnboundEvidence(t *testing.T) {
	for name, change := range map[string]string{
		probePolicyRevisionEnvironment: "PENDING-APPROVAL",
		probeLarkSkillSHAEnvironment:   "",
		probePodUIDEnvironment:         "",
		probeConnectivityEnvironment:   "101",
		probeLifecycleEnvironment:      "6",
	} {
		t.Run(name, func(t *testing.T) {
			values := validNetworkProbeEnvironment()
			values[name] = change
			if _, err := loadNetworkProbeConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("unbound probe configuration was accepted")
			}
		})
	}
}

const testBkectlRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testProbeFiles(cli, skill []byte) map[string][]byte {
	return map[string][]byte{
		probeLarkCLIPath:              cli,
		probeLarkSkillPath:            skill,
		probeManagedSkillPath:         []byte("# managed CLI skill\n"),
		probeBkectlCLIPath:            []byte("fake pinned bkectl"),
		probeBkectlSkillPath:          []byte("# bkectl skill\n"),
		probeBkectlCommandSurfacePath: []byte("# command surface\n"),
		probeBkectlDomainGuidesPath:   []byte("# domain guides\n"),
		probeBkectlInvocationPath:     []byte("# invocation\n"),
	}
}

func testNetworkProbeConfig(files map[string][]byte) networkProbeConfig {
	sequence := 0
	digest := func(value []byte) string {
		sum := sha256.Sum256(value)
		return hex.EncodeToString(sum[:])
	}
	return networkProbeConfig{
		provider: providerConfig{
			region: "i18n-tt", controlPlaneURL: "https://" + adapter.SGTAEControlPlaneHost,
			controlPlaneHost: adapter.SGTAEControlPlaneHost, dataPlaneSuffix: adapter.SGTAEDomainSuffix,
			byteCloudSite: adapter.ByteCloudSiteI18NTT, jwtEndpoint: adapter.ByteCloudJWTEndpointSG,
			proxyURL:       adapter.TAEProxyURLSG,
			controlTimeout: time.Second, jwtRequestTimeout: time.Second,
			sandboxImage: "registry.example/sandbox:sha256-" + strings.Repeat("1", 64),
			sandboxID:    "sandbox-1", sandboxRevisionID: "revision-1",
		},
		policyRevision: "revision-1",
		larkCLIVersion: "test", larkCLISHA256: digest(files[probeLarkCLIPath]), larkCLISize: int64(len(files[probeLarkCLIPath])),
		larkSkillSHA256:    digest(files[probeLarkSkillPath]),
		managedSkillSHA256: digest(files[probeManagedSkillPath]), managedSkillSize: int64(len(files[probeManagedSkillPath])),
		bkectlSourceRevision: testBkectlRevision,
		bkectlCLISHA256:      digest(files[probeBkectlCLIPath]), bkectlCLISize: int64(len(files[probeBkectlCLIPath])),
		bkectlSkillPackSHA256: digest([]byte("test bkectl skill pack")),
		bkectlSkillSHA256:     digest(files[probeBkectlSkillPath]), bkectlSkillSize: int64(len(files[probeBkectlSkillPath])),
		bkectlCommandSurfaceSHA256: digest(files[probeBkectlCommandSurfacePath]), bkectlCommandSurfaceSize: int64(len(files[probeBkectlCommandSurfacePath])),
		bkectlDomainGuidesSHA256: digest(files[probeBkectlDomainGuidesPath]), bkectlDomainGuidesSize: int64(len(files[probeBkectlDomainGuidesPath])),
		bkectlInvocationSHA256: digest(files[probeBkectlInvocationPath]), bkectlInvocationSize: int64(len(files[probeBkectlInvocationPath])),
		connectivityAttempts: 2, lifecycleAttempts: 1,
		readyTimeout: time.Second,
		source: taenetworkreport.Source{
			Namespace: "agentserver", PodName: "probe-pod", PodUID: "12345678-1234-4234-8234-123456789abc",
			NodeName: "sg-node-1", ServiceAccount: "sandbox-gateway",
		},
		newID: func() (string, error) {
			sequence++
			return strings.Repeat(strconvHex(sequence), 32), nil
		},
		now: func() time.Time { return time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC) },
	}
}

func strconvHex(value int) string {
	const digits = "0123456789abcdef"
	return string(digits[value%len(digits)])
}

func validNetworkProbeEnvironment() map[string]string {
	values := validProviderEnvironment()
	values[probePolicyRevisionEnvironment] = "revision-1"
	values[probeLarkSkillSHAEnvironment] = strings.Repeat("2", 64)
	values[probeConnectivityEnvironment] = "1"
	values[probeLifecycleEnvironment] = "1"
	values[probeNamespaceEnvironment] = "agentserver"
	values[probePodNameEnvironment] = "tae-network-probe-abcde"
	values[probePodUIDEnvironment] = "12345678-1234-4234-8234-123456789abc"
	values[probeNodeNameEnvironment] = "sg-node-1"
	values[probeServiceAccountEnvironment] = "sandbox-gateway"
	return values
}

func checksByName(checks []taenetworkreport.Check) map[string]taenetworkreport.Check {
	result := make(map[string]taenetworkreport.Check, len(checks))
	for _, check := range checks {
		result[check.Name] = check
	}
	return result
}

type probeControl struct {
	session     adapter.ControlSession
	deleted     bool
	createError error
	omitCommand bool
}

func (control *probeControl) Create(_ context.Context, input adapter.CreateInput) (adapter.ControlSession, error) {
	if control.createError != nil {
		return adapter.ControlSession{}, control.createError
	}
	control.deleted = false
	command := managedruntime.ExecutablePath
	if control.omitCommand {
		command = ""
	}
	control.session = adapter.ControlSession{
		ID: "tae-probe-session", Status: "running", ExpiresAt: time.Now().Add(input.TTL),
		SandboxdEnabled: true, Command: command, Metadata: cloneTestStrings(input.Metadata),
	}
	return control.session, nil
}

func (control *probeControl) Get(context.Context, string) (adapter.ControlSession, error) {
	if control.session.ID == "" {
		return adapter.ControlSession{}, adapter.ErrSessionNotFound
	}
	session := control.session
	session.Deleted = control.deleted
	return session, nil
}

func (control *probeControl) Search(_ context.Context, input adapter.SearchInput) (adapter.SearchResult, error) {
	value := input.Metadata[adapter.MetadataSandboxID]
	if strings.Contains(value, "missing-") || control.session.ID == "" ||
		control.session.Metadata[adapter.MetadataSandboxID] != value {
		return adapter.SearchResult{}, nil
	}
	session := control.session
	session.Deleted = control.deleted
	return adapter.SearchResult{Sessions: []adapter.ControlSession{session}, Total: 1}, nil
}

func (control *probeControl) UpdateTTL(context.Context, string, time.Duration) error { return nil }

func (control *probeControl) Delete(context.Context, string) error {
	if control.session.ID == "" {
		return adapter.ErrSessionNotFound
	}
	control.deleted = true
	return nil
}

type probeData struct {
	files      map[string][]byte
	startError error
}

func (data *probeData) StartProcess(_ context.Context, _ string, input adapter.StartProcessInput) (adapter.EventStream, error) {
	if data.startError != nil {
		return nil, data.startError
	}
	var stdout string
	switch input.Executable {
	case "/bin/sh":
		if !reflect.DeepEqual(input.Arguments, []string{"-lc", "printf terminal-ok"}) || input.WorkingDirectory != probeWorkspacePath {
			return nil, errors.New("unexpected terminal probe request")
		}
		stdout = "terminal-ok"
	case probeLarkCLIPath:
		if !reflect.DeepEqual(input.Arguments, []string{"--version"}) || input.WorkingDirectory != probeWorkspacePath {
			return nil, errors.New("unexpected lark version probe request")
		}
		stdout = "lark-cli version test\n"
	case probeBkectlCLIPath:
		if !reflect.DeepEqual(input.Arguments, []string{"--json", "version"}) || input.WorkingDirectory != probeWorkspacePath {
			return nil, errors.New("unexpected bkectl version probe request")
		}
		stdout = `{ "data": { "version": "` + testBkectlRevision + `", "build_time": "1970-01-01T00:00:00Z" }, "success": true, "error": null }` + "\n"
	default:
		return nil, errors.New("unexpected probe executable")
	}
	return &probeEventStream{events: []adapter.StreamEvent{
		{Name: "process.start", Data: map[string]any{"pid": 123}},
		{Name: "process.data", Data: map[string]any{"stdout": stdout}},
		{Name: "process.exit", Data: map[string]any{"exit_code": 0}},
	}}, nil
}

func (data *probeData) ConnectProcess(context.Context, string, int) (adapter.EventStream, error) {
	return nil, errors.New("unexpected connect")
}

func (data *probeData) SignalProcess(context.Context, string, int, int) (string, error) {
	return "", errors.New("unexpected signal")
}

func (data *probeData) Stat(_ context.Context, _ string, path string) (adapter.FileInfo, string, error) {
	contents, found := data.files[path]
	if !found {
		return adapter.FileInfo{}, "", errors.New("unexpected path")
	}
	return adapter.FileInfo{Type: "file", Size: int64(len(contents))}, "", nil
}

func (data *probeData) Download(_ context.Context, _ string, path string) (adapter.Download, error) {
	contents, found := data.files[path]
	if !found {
		return adapter.Download{}, errors.New("unexpected path")
	}
	return adapter.Download{Body: io.NopCloser(bytes.NewReader(contents)), ContentLength: int64(len(contents))}, nil
}

type probeEventStream struct {
	events []adapter.StreamEvent
	index  int
}

func (stream *probeEventStream) Next(context.Context) (adapter.StreamEvent, error) {
	if stream.index >= len(stream.events) {
		return adapter.StreamEvent{}, io.EOF
	}
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}

func (stream *probeEventStream) RequestID() string { return "probe-request" }
func (stream *probeEventStream) Close() error      { return nil }

func cloneTestStrings(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
