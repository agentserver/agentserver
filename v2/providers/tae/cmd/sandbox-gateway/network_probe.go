package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/productionimage"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

const (
	probePSM                             = "bytedance.sandbox.agentserver"
	probeRegion                          = "sg"
	probeDeploymentSHAEnvironment        = "AGENTSERVER_V2_TAE_PROBE_DEPLOYMENT_CONFIG_SHA256"
	probePolicyRevisionEnvironment       = "AGENTSERVER_V2_TAE_PROBE_POLICY_REVISION"
	probeLarkSkillSHAEnvironment         = "AGENTSERVER_V2_TAE_PROBE_LARK_SKILL_SHA256"
	probeConnectivityEnvironment         = "AGENTSERVER_V2_TAE_PROBE_CONNECTIVITY_ATTEMPTS"
	probeLifecycleEnvironment            = "AGENTSERVER_V2_TAE_PROBE_LIFECYCLE_ATTEMPTS"
	probeReadyTimeoutEnvironment         = "AGENTSERVER_V2_TAE_PROBE_READY_TIMEOUT"
	probeNamespaceEnvironment            = "AGENTSERVER_V2_TAE_PROBE_POD_NAMESPACE"
	probePodNameEnvironment              = "AGENTSERVER_V2_TAE_PROBE_POD_NAME"
	probePodUIDEnvironment               = "AGENTSERVER_V2_TAE_PROBE_POD_UID"
	probeNodeNameEnvironment             = "AGENTSERVER_V2_TAE_PROBE_NODE_NAME"
	probeServiceAccountEnvironment       = "AGENTSERVER_V2_TAE_PROBE_SERVICE_ACCOUNT"
	probeMetadataPrefix                  = "agentserver-tae-network-probe-"
	probeLarkCLIPath                     = "/usr/local/bin/lark-cli"
	probeLarkSkillPath                   = "/opt/agentserver/packs/lark-readonly/SKILL.md"
	probeWorkspacePath                   = "/workspace"
	probeMaximumSkillBytes         int64 = 256 * 1024
	probeMaximumProcessOutput            = 4096
	probeSessionTTL                      = 15 * time.Minute
	probeProcessTimeout                  = 60 * time.Second
	probeCleanupTimeout                  = 2 * time.Minute
)

var probeRevisionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._:-]{0,127}$`)

type networkProbeConfig struct {
	provider             providerConfig
	deploymentSHA256     string
	policyRevision       string
	larkCLIVersion       string
	larkCLISHA256        string
	larkCLISize          int64
	larkSkillSHA256      string
	connectivityAttempts int
	lifecycleAttempts    int
	readyTimeout         time.Duration
	source               taenetworkreport.Source
	newID                func() (string, error)
	now                  func() time.Time
}

type taeClientsFactory func(context.Context, providerConfig, string) (*taeClients, error)

func probeNetwork(ctx context.Context, getenv func(string) string, output io.Writer) (bool, error) {
	return probeNetworkWithFactory(ctx, getenv, output, newTAEClients)
}

func probeNetworkWithFactory(ctx context.Context, getenv func(string) string, output io.Writer, factory taeClientsFactory) (bool, error) {
	if ctx == nil || getenv == nil || output == nil || factory == nil {
		return false, errors.New("TAE network probe runtime is unavailable")
	}
	config, err := loadNetworkProbeConfig(getenv)
	if err != nil {
		return false, err
	}
	clients, err := factory(ctx, config.provider, probePSM)
	if err != nil {
		return false, err
	}
	defer clients.Close()
	report, err := executeNetworkProbe(ctx, config, clients)
	if err != nil {
		return false, err
	}
	raw, err := taenetworkreport.Marshal(report)
	if err != nil {
		return false, fmt.Errorf("finalize TAE network report: %w", err)
	}
	written, err := output.Write(raw)
	if err != nil || written != len(raw) {
		return false, errors.New("write complete TAE network report")
	}
	return report.Passed, nil
}

func loadNetworkProbeConfig(getenv func(string) string) (networkProbeConfig, error) {
	provider, err := loadProviderConfig(getenv)
	if err != nil {
		return networkProbeConfig{}, err
	}
	deploymentSHA256 := getenv(probeDeploymentSHAEnvironment)
	if !nonzeroProbeDigest(deploymentSHA256) {
		return networkProbeConfig{}, fmt.Errorf("%s must be a non-zero lowercase SHA-256", probeDeploymentSHAEnvironment)
	}
	policyRevision := getenv(probePolicyRevisionEnvironment)
	if !probeRevisionPattern.MatchString(policyRevision) || containsProbeSentinel(policyRevision) {
		return networkProbeConfig{}, fmt.Errorf("%s must be the actual published TAE policy revision", probePolicyRevisionEnvironment)
	}
	larkSkillSHA256 := getenv(probeLarkSkillSHAEnvironment)
	if !nonzeroProbeDigest(larkSkillSHA256) {
		return networkProbeConfig{}, fmt.Errorf("%s must be a non-zero lowercase SHA-256", probeLarkSkillSHAEnvironment)
	}
	connectivityAttempts, err := optionalInt(getenv(probeConnectivityEnvironment), 20, 1, 100, probeConnectivityEnvironment)
	if err != nil {
		return networkProbeConfig{}, err
	}
	lifecycleAttempts, err := optionalInt(getenv(probeLifecycleEnvironment), 1, 1, 5, probeLifecycleEnvironment)
	if err != nil {
		return networkProbeConfig{}, err
	}
	readyTimeout, err := optionalDuration(getenv(probeReadyTimeoutEnvironment), 3*time.Minute, 30*time.Second, 10*time.Minute, probeReadyTimeoutEnvironment)
	if err != nil {
		return networkProbeConfig{}, err
	}
	source := taenetworkreport.Source{
		Namespace: getenv(probeNamespaceEnvironment), PodName: getenv(probePodNameEnvironment),
		PodUID: getenv(probePodUIDEnvironment), NodeName: getenv(probeNodeNameEnvironment),
		ServiceAccount: getenv(probeServiceAccountEnvironment),
	}
	// Validate the downward-API identity before any provider request. A report
	// with an operator-supplied blank source is not acceptable release evidence.
	for name, value := range map[string]string{
		probeNamespaceEnvironment: source.Namespace, probePodNameEnvironment: source.PodName,
		probePodUIDEnvironment: source.PodUID, probeNodeNameEnvironment: source.NodeName,
		probeServiceAccountEnvironment: source.ServiceAccount,
	} {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 253 || strings.ContainsAny(value, "\r\n\x00") {
			return networkProbeConfig{}, fmt.Errorf("%s must be injected from the Kubernetes downward API", name)
		}
	}
	return networkProbeConfig{
		provider: provider, deploymentSHA256: deploymentSHA256, policyRevision: policyRevision,
		larkCLIVersion: productionimage.ManagedLarkCLIVersion,
		larkCLISHA256:  productionimage.ManagedLarkCLISHA256, larkCLISize: productionimage.ManagedLarkCLISizeBytes,
		larkSkillSHA256: larkSkillSHA256, connectivityAttempts: connectivityAttempts,
		lifecycleAttempts: lifecycleAttempts, readyTimeout: readyTimeout, source: source,
		newID: newProbeID, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func executeNetworkProbe(ctx context.Context, config networkProbeConfig, clients *taeClients) (taenetworkreport.Report, error) {
	if clients == nil || clients.refresh == nil || clients.control == nil || clients.data == nil || config.newID == nil || config.now == nil {
		return taenetworkreport.Report{}, errors.New("TAE network probe clients are unavailable")
	}
	startedAt := config.now()
	recorder := newProbeRecorder()
	for attempt := 0; attempt < config.connectivityAttempts; attempt++ {
		_ = recorder.run("jwt_force_refresh", func() error {
			requestContext, cancel := context.WithTimeout(ctx, config.provider.jwtRequestTimeout)
			defer cancel()
			return clients.refresh(requestContext)
		})
	}
	for attempt := 0; attempt < config.connectivityAttempts; attempt++ {
		probeID, err := config.newID()
		if err != nil {
			return taenetworkreport.Report{}, errors.New("generate TAE network probe identity")
		}
		_ = recorder.run("control_search_missing", func() error {
			requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
			defer cancel()
			result, err := clients.control.Search(requestContext, adapter.SearchInput{
				Metadata: map[string]string{adapter.MetadataSandboxID: probeMetadataPrefix + "missing-" + probeID}, Limit: 1,
			})
			if err != nil {
				return err
			}
			if result.Total != 0 || len(result.Sessions) != 0 {
				return newProbeFailure("unexpected_search_match")
			}
			return nil
		})
	}
	cleanupConfirmed := true
	for attempt := 0; attempt < config.lifecycleAttempts; attempt++ {
		confirmed, err := runProbeLifecycle(ctx, config, clients, recorder, attempt)
		if err != nil {
			return taenetworkreport.Report{}, err
		}
		cleanupConfirmed = cleanupConfirmed && confirmed
	}
	checks := recorder.results()
	passed := cleanupConfirmed
	for _, check := range checks {
		if check.Failed != 0 {
			passed = false
		}
	}
	domainSuffix, err := adapter.SGDataplaneDomainSuffix()
	if err != nil {
		return taenetworkreport.Report{}, errors.New("resolve SG TAE data-plane domain for report")
	}
	return taenetworkreport.Report{
		SchemaVersion: taenetworkreport.CurrentVersion, Kind: taenetworkreport.Kind,
		StartedAt: startedAt, FinishedAt: config.now(), Passed: passed, CleanupConfirmed: cleanupConfirmed,
		Source: config.source,
		Configuration: taenetworkreport.Configuration{
			DeploymentConfigSHA256: config.deploymentSHA256, Region: probeRegion, PSM: probePSM,
			PolicyRevision: config.policyRevision, ByteCloudSite: adapter.ByteCloudSiteI18NTT,
			JWTEndpoint: adapter.ByteCloudJWTEndpointSG, ProxyURL: adapter.TAEProxyURLSG,
			ControlPlaneHost: adapter.SGTAEControlPlaneHost, DataPlaneDomainSuffix: domainSuffix,
			SandboxImage: config.provider.sandboxImage, LarkCLIVersion: config.larkCLIVersion,
			LarkCLISHA256: config.larkCLISHA256, LarkSkillSHA256: config.larkSkillSHA256,
			ConnectivityAttempts: config.connectivityAttempts, LifecycleAttempts: config.lifecycleAttempts,
		},
		Checks: checks,
	}, nil
}

func runProbeLifecycle(ctx context.Context, config networkProbeConfig, clients *taeClients, recorder *probeRecorder, attempt int) (bool, error) {
	probeID, err := config.newID()
	if err != nil {
		return false, errors.New("generate TAE lifecycle probe identity")
	}
	metadata := map[string]string{adapter.MetadataSandboxID: probeMetadataPrefix + probeID}
	var session adapter.ControlSession
	createErr := recorder.run("control_create", func() error {
		requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
		defer cancel()
		created, err := clients.control.Create(requestContext, adapter.CreateInput{
			TTL: probeSessionTTL, Metadata: metadata,
		})
		if err != nil {
			return err
		}
		if created.ID == "" || created.Deleted || created.Metadata[adapter.MetadataSandboxID] != metadata[adapter.MetadataSandboxID] {
			return newProbeFailure("invalid_create_response")
		}
		session = created
		return nil
	})
	if createErr == nil {
		_ = recorder.run("control_search_created", func() error {
			requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
			defer cancel()
			result, err := clients.control.Search(requestContext, adapter.SearchInput{Metadata: metadata, Limit: 2})
			if err != nil {
				return err
			}
			if result.Total != 1 || len(result.Sessions) != 1 || result.Sessions[0].ID != session.ID || result.Sessions[0].Deleted {
				return newProbeFailure("created_session_not_unique")
			}
			return nil
		})
		readyErr := recorder.run("control_wait_ready", func() error {
			ready, err := waitForProbeReady(ctx, clients.control, session.ID, config.readyTimeout, config.provider.controlTimeout)
			if err == nil {
				session = ready
			}
			return err
		})
		if readyErr == nil {
			_ = recorder.run("control_update_ttl", func() error {
				requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
				defer cancel()
				return clients.control.UpdateTTL(requestContext, session.ID, probeSessionTTL)
			})
			_ = recorder.run("data_exec_lark_version", func() error {
				return probeLarkVersion(ctx, clients.data, session.ID, probeID, attempt, config.larkCLIVersion)
			})
			_ = recorder.run("data_stat_lark_cli", func() error {
				requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
				defer cancel()
				info, _, err := clients.data.Stat(requestContext, session.ID, probeLarkCLIPath)
				if err != nil {
					return err
				}
				if info.Type != "file" || info.Size != config.larkCLISize || info.SymlinkTarget != "" {
					return newProbeFailure("lark_cli_stat_mismatch")
				}
				return nil
			})
			var cliBytes int64
			_ = recorder.run("data_read_lark_cli", func() error {
				var readErr error
				cliBytes, readErr = probeDownloadDigest(ctx, clients.data, session.ID, probeLarkCLIPath, config.larkCLISize, config.larkCLISHA256)
				return readErr
			})
			recorder.addBytes("data_read_lark_cli", cliBytes)
			var skillSize int64
			_ = recorder.run("data_stat_lark_skill", func() error {
				requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
				defer cancel()
				info, _, err := clients.data.Stat(requestContext, session.ID, probeLarkSkillPath)
				if err != nil {
					return err
				}
				if info.Type != "file" || info.Size < 1 || info.Size > probeMaximumSkillBytes || info.SymlinkTarget != "" {
					return newProbeFailure("lark_skill_stat_mismatch")
				}
				skillSize = info.Size
				return nil
			})
			if skillSize > 0 {
				var skillBytes int64
				_ = recorder.run("data_read_lark_skill", func() error {
					var readErr error
					skillBytes, readErr = probeDownloadDigest(ctx, clients.data, session.ID, probeLarkSkillPath, skillSize, config.larkSkillSHA256)
					return readErr
				})
				recorder.addBytes("data_read_lark_skill", skillBytes)
			}
		}
		deleteErr := recorder.run("control_delete", func() error {
			requestContext, cancel := context.WithTimeout(ctx, config.provider.controlTimeout)
			defer cancel()
			return clients.control.Delete(requestContext, session.ID)
		})
		if deleteErr == nil {
			_ = recorder.run("control_confirm_deleted", func() error {
				return confirmProbeDeleted(ctx, clients.control, session.ID, config.readyTimeout, config.provider.controlTimeout)
			})
		}
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), probeCleanupTimeout)
	defer cancelCleanup()
	cleanupErr := recorder.run("control_cleanup", func() error {
		return cleanupProbeSessions(cleanupContext, clients.control, metadata, config.provider.controlTimeout)
	})
	return cleanupErr == nil, nil
}

func waitForProbeReady(ctx context.Context, control adapter.ControlPlane, sessionID string, timeout, requestTimeout time.Duration) (adapter.ControlSession, error) {
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		requestContext, cancelRequest := context.WithTimeout(waitContext, requestTimeout)
		session, err := control.Get(requestContext, sessionID)
		cancelRequest()
		if err != nil {
			return adapter.ControlSession{}, err
		}
		if session.Deleted {
			return adapter.ControlSession{}, newProbeFailure("session_deleted_before_ready")
		}
		if session.SandboxdEnabled {
			return session, nil
		}
		select {
		case <-waitContext.Done():
			return adapter.ControlSession{}, newProbeFailure("session_ready_timeout")
		case <-time.After(time.Second):
		}
	}
}

func confirmProbeDeleted(ctx context.Context, control adapter.ControlPlane, sessionID string, timeout, requestTimeout time.Duration) error {
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		requestContext, cancelRequest := context.WithTimeout(waitContext, requestTimeout)
		session, err := control.Get(requestContext, sessionID)
		cancelRequest()
		if errors.Is(err, adapter.ErrSessionNotFound) || (err == nil && session.Deleted) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-waitContext.Done():
			return newProbeFailure("session_delete_timeout")
		case <-time.After(time.Second):
		}
	}
}

func cleanupProbeSessions(ctx context.Context, control adapter.ControlPlane, metadata map[string]string, requestTimeout time.Duration) error {
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	result, err := control.Search(requestContext, adapter.SearchInput{Metadata: metadata, Limit: 100})
	cancel()
	if err != nil {
		return err
	}
	if result.Total > len(result.Sessions) {
		return newProbeFailure("cleanup_search_incomplete")
	}
	for _, session := range result.Sessions {
		if session.Deleted || session.Metadata[adapter.MetadataSandboxID] != metadata[adapter.MetadataSandboxID] {
			continue
		}
		requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
		err := control.Delete(requestContext, session.ID)
		cancel()
		if err != nil && !errors.Is(err, adapter.ErrSessionNotFound) {
			return err
		}
		if err := confirmProbeDeleted(ctx, control, session.ID, probeCleanupTimeout, requestTimeout); err != nil {
			return err
		}
	}
	return nil
}

func probeLarkVersion(ctx context.Context, data adapter.DataPlane, sessionID, probeID string, attempt int, version string) error {
	requestContext, cancel := context.WithTimeout(ctx, probeProcessTimeout+15*time.Second)
	defer cancel()
	stream, err := data.StartProcess(requestContext, sessionID, adapter.StartProcessInput{
		RequestID:  "agentserver-tae-probe-" + probeID + "-" + strconv.Itoa(attempt),
		Executable: probeLarkCLIPath, Arguments: []string{"--version"}, WorkingDirectory: probeWorkspacePath,
		Environment: map[string]string{}, Timeout: probeProcessTimeout,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	started := false
	exited := false
	var stdout, stderr strings.Builder
	for !exited {
		event, err := stream.Next(requestContext)
		if err != nil {
			return err
		}
		switch event.Name {
		case "process.start":
			pid, ok := probeInteger(event.Data["pid"])
			if started || !ok || pid < 1 {
				return newProbeFailure("invalid_process_start")
			}
			started = true
		case "process.data":
			if !started {
				return newProbeFailure("process_data_before_start")
			}
			out, outOK := event.Data["stdout"].(string)
			errOut, errOK := event.Data["stderr"].(string)
			if !outOK && !errOK {
				return newProbeFailure("invalid_process_data")
			}
			if stdout.Len()+stderr.Len()+len(out)+len(errOut) > probeMaximumProcessOutput {
				return newProbeFailure("process_output_too_large")
			}
			stdout.WriteString(out)
			stderr.WriteString(errOut)
		case "process.exit":
			exitCode, ok := probeInteger(event.Data["exit_code"])
			if !ok {
				exitCode, ok = probeInteger(event.Data["exitCode"])
			}
			if !started || !ok || exitCode != 0 {
				return newProbeFailure("lark_version_process_failed")
			}
			exited = true
		}
	}
	if strings.TrimSpace(stdout.String()) != "lark-cli version "+version || strings.TrimSpace(stderr.String()) != "" {
		return newProbeFailure("lark_version_output_mismatch")
	}
	return nil
}

func probeDownloadDigest(ctx context.Context, data adapter.DataPlane, sessionID, path string, size int64, expectedDigest string) (int64, error) {
	if size < 1 || size > 128*1024*1024 || !nonzeroProbeDigest(expectedDigest) {
		return 0, newProbeFailure("invalid_download_expectation")
	}
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	download, err := data.Download(requestContext, sessionID, path)
	if err != nil {
		return 0, err
	}
	if download.Body == nil {
		return 0, newProbeFailure("download_body_missing")
	}
	if download.ContentLength >= 0 && download.ContentLength != size {
		_ = download.Body.Close()
		return 0, newProbeFailure("download_length_mismatch")
	}
	hash := sha256.New()
	read, readErr := io.Copy(hash, io.LimitReader(download.Body, size+1))
	closeErr := download.Body.Close()
	if readErr != nil || closeErr != nil {
		return read, newProbeFailure("download_stream_failed")
	}
	if read != size || hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return read, newProbeFailure("download_digest_mismatch")
	}
	return read, nil
}

func probeInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		parsed := int64(typed)
		return parsed, float64(parsed) == typed
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

type probeFailure struct{ code string }

func (failure *probeFailure) Error() string { return failure.code }

func newProbeFailure(code string) error { return &probeFailure{code: code} }

type probeRecorder struct {
	order  []string
	checks map[string]*taenetworkreport.Check
}

func newProbeRecorder() *probeRecorder {
	return &probeRecorder{checks: make(map[string]*taenetworkreport.Check)}
}

func (recorder *probeRecorder) run(name string, operation func() error) error {
	check := recorder.check(name)
	started := time.Now()
	err := operation()
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	check.Attempts++
	check.DurationsMillis = append(check.DurationsMillis, duration)
	if err == nil {
		check.Succeeded++
		return nil
	}
	check.Failed++
	if check.Errors == nil {
		check.Errors = make(map[string]int)
	}
	check.Errors[classifyProbeError(err)]++
	return err
}

func (recorder *probeRecorder) addBytes(name string, count int64) {
	if count > 0 {
		recorder.check(name).BytesRead += count
	}
}

func (recorder *probeRecorder) check(name string) *taenetworkreport.Check {
	if existing := recorder.checks[name]; existing != nil {
		return existing
	}
	check := &taenetworkreport.Check{Name: name}
	recorder.checks[name] = check
	recorder.order = append(recorder.order, name)
	return check
}

func (recorder *probeRecorder) results() []taenetworkreport.Check {
	results := make([]taenetworkreport.Check, 0, len(recorder.order))
	for _, name := range recorder.order {
		original := recorder.checks[name]
		clone := *original
		clone.DurationsMillis = append([]int64(nil), original.DurationsMillis...)
		if original.Errors != nil {
			clone.Errors = make(map[string]int, len(original.Errors))
			for code, count := range original.Errors {
				clone.Errors[code] = count
			}
		}
		results = append(results, clone)
	}
	return results
}

func classifyProbeError(err error) string {
	var failure *probeFailure
	if errors.As(err, &failure) && failure != nil && errorCodeSafe(failure.code) {
		return failure.code
	}
	var requestError *adapter.RequestError
	if errors.As(err, &requestError) && requestError != nil && errorCodeSafe(requestError.Code) {
		return requestError.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if errors.Is(err, adapter.ErrSessionNotFound) {
		return "not_found"
	}
	return "probe_failed"
}

func errorCodeSafe(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func newProbeID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func nonzeroProbeDigest(value string) bool {
	if len(value) != 64 || strings.Trim(value, "0") == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func containsProbeSentinel(value string) bool {
	upper := strings.ToUpper(value)
	for _, sentinel := range []string{"TODO", "TBD", "REPLACE", "EXAMPLE", "PENDING"} {
		if strings.Contains(upper, sentinel) {
			return true
		}
	}
	return false
}
