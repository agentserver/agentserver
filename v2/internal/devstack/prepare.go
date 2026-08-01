package devstack

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devfixtures"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	manifestSigningKeyID = "insecure-dev-manifest-v1"
	workerServiceAccount = "harness-worker"

	coreEnvironmentFile             = "env/agentserver-core.env"
	browserEnvironmentFile          = "env/browser-gateway.env"
	executorEnvironmentFile         = "env/executor-gateway.env"
	harnessPoolEnvironmentFile      = "env/harness-pool.env"
	bootstrapConfigFile             = "config/core-bootstrap.json"
	workerDeploymentConfigFile      = "config/harness-worker.json"
	manifestVerificationKeyringFile = "config/run-manifest-keyring.json"
	developmentFixturesConfigFile   = devfixtures.RelativeConfigPath
	agentxLaunchFile                = "agentx/launch.json"
	metadataFile                    = "metadata.json"

	certificateAuthorityFile = "pki/ca.pem"
	capabilityKeyFile        = "secrets/run-capability.key"
	cursorKeyFile            = "secrets/run-cursor.key"
	manifestSeedFile         = "secrets/run-manifest.seed"
	browserBearerTokenFile   = devfixtures.RelativeBrowserBearerTokenPath

	objectDirectory            = "state/objects"
	harnessRuntimeDirectory    = "state/harness-runtime"
	checkpointStagingDirectory = "state/checkpoint-staging"
	agentxRuntimeDirectory     = "state/agentx-runtime"
)

var outputDirectories = []string{
	"config", "env", "pki", "secrets", "state", "agentx",
	objectDirectory, harnessRuntimeDirectory, checkpointStagingDirectory, agentxRuntimeDirectory,
}

type Result struct {
	OutputDirectory      string
	MetadataFile         string
	BootstrapConfigFile  string
	WorkerDeploymentFile string
	AgentxLaunchFile     string
	FixturesConfigFile   string
	BrowserBearerFile    string
	EnvironmentFiles     map[string]string
}

type coreBootstrapDocument struct {
	Version     int                           `json:"version"`
	WorkspaceID string                        `json:"workspaceId"`
	SessionID   string                        `json:"sessionId"`
	ActorID     string                        `json:"actorId"`
	Executor    coreBootstrapExecutorDocument `json:"executor"`
}

type coreBootstrapExecutorDocument struct {
	ExecutorID          string `json:"executorId"`
	EnvironmentID       string `json:"environmentId"`
	AgentxVersion       string `json:"agentxVersion"`
	Platform            string `json:"platform"`
	RuntimeManifestFile string `json:"runtimeManifestFile"`
	WorkspaceRoot       string `json:"workspaceRoot"`
	DisplayName         string `json:"displayName,omitempty"`
	Description         string `json:"description,omitempty"`
	DefaultCWD          string `json:"defaultCwd,omitempty"`
}

type workerDeploymentDocument struct {
	Version                int                    `json:"version"`
	RunManifestKeyringFile string                 `json:"runManifestKeyringFile"`
	RuntimeManifestFile    string                 `json:"runtimeManifestFile"`
	RuntimeBundleRoot      string                 `json:"runtimeBundleRoot"`
	FinalExec              workerArtifactDocument `json:"finalExec"`
	CodexConfigProfile     string                 `json:"codexConfigProfile"`
	WorkerUID              uint32                 `json:"workerUid"`
	WorkerGID              uint32                 `json:"workerGid"`
	AppUID                 uint32                 `json:"appUid"`
	AppGID                 uint32                 `json:"appGid"`
	TLS                    workerTLSDocument      `json:"tls"`
}

type workerArtifactDocument struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type workerTLSDocument struct {
	CAFile          string `json:"caFile"`
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type agentxLaunchDocument struct {
	Version   int      `json:"version"`
	Mode      string   `json:"mode"`
	Program   string   `json:"program"`
	Arguments []string `json:"arguments"`
}

type metadataDocument struct {
	Version       int                       `json:"version"`
	Mode          string                    `json:"mode"`
	CreatedAt     string                    `json:"createdAt"`
	Authority     metadataAuthorityDocument `json:"authority"`
	Runtime       metadataRuntimeDocument   `json:"runtime"`
	Endpoints     metadataEndpointDocument  `json:"endpoints"`
	Files         metadataFilesDocument     `json:"files"`
	TLSIdentities map[string]string         `json:"tlsIdentities"`
	Warnings      []string                  `json:"warnings"`
}

type metadataAuthorityDocument struct {
	WorkspaceID   string `json:"workspaceId"`
	SessionID     string `json:"sessionId"`
	ActorID       string `json:"actorId"`
	ExecutorID    string `json:"executorId"`
	EnvironmentID string `json:"environmentId"`
}

type metadataRuntimeDocument struct {
	Platform                   string `json:"platform"`
	CodexRelease               string `json:"codexRelease"`
	RuntimeManifestSHA256      string `json:"runtimeManifestSha256"`
	CheckpointAllowlistVersion int    `json:"checkpointAllowlistVersion"`
}

type metadataEndpointDocument struct {
	Core                 string `json:"core"`
	BrowserGateway       string `json:"browserGateway"`
	AGUIEndpointTemplate string `json:"aguiEndpointTemplate"`
	ExecutorGateway      string `json:"executorGateway"`
	ExecutorMCP          string `json:"executorMcp"`
	AgentxGateway        string `json:"agentxGateway"`
	HarnessPool          string `json:"harnessPool"`
	HydraIntrospection   string `json:"hydraIntrospection"`
	LLMProxy             string `json:"llmproxy"`
}

type metadataFilesDocument struct {
	CertificateAuthority string            `json:"certificateAuthority"`
	BootstrapConfig      string            `json:"bootstrapConfig"`
	WorkerDeployment     string            `json:"workerDeployment"`
	AgentxLaunch         string            `json:"agentxLaunch"`
	Environment          map[string]string `json:"environment"`
	LLMProxyCertificate  string            `json:"llmproxyCertificate"`
	LLMProxyPrivateKey   string            `json:"llmproxyPrivateKey"`
	RunCapabilityKey     string            `json:"runCapabilityKey"`
	DevelopmentFixtures  string            `json:"developmentFixtures"`
	BrowserBearerToken   string            `json:"browserBearerToken"`
}

func PrepareFromFile(configPath, outputDirectory string) (Result, error) {
	loaded, err := LoadConfig(configPath)
	if err != nil {
		return Result{}, err
	}
	return Prepare(loaded, outputDirectory, rand.Reader, time.Now().UTC())
}

func Prepare(config LoadedConfig, outputDirectory string, random io.Reader, now time.Time) (_ Result, returnErr error) {
	validated, err := ValidateConfig(config.Document)
	if err != nil {
		return Result{}, err
	}
	// The caller cannot swap in a separately parsed manifest after config
	// validation. Re-read and re-derive every runtime fact immediately before
	// rendering the bundle.
	config = validated
	if err := validateOutputDirectory(outputDirectory); err != nil {
		return Result{}, err
	}
	if random == nil || now.IsZero() {
		return Result{}, errors.New("development stack preparation requires randomness and a clock")
	}

	pki, err := generateDevelopmentPKI(random, now.UTC())
	if err != nil {
		return Result{}, err
	}
	capabilityKey, err := randomSecret(random, 32, "run capability HMAC")
	if err != nil {
		return Result{}, err
	}
	defer clear(capabilityKey)
	browserBearerEntropy, err := randomSecret(random, 32, "browser bearer")
	if err != nil {
		return Result{}, err
	}
	defer clear(browserBearerEntropy)
	cursorKey, err := randomSecret(random, 32, "run cursor HMAC")
	if err != nil {
		return Result{}, err
	}
	defer clear(cursorKey)
	manifestPublic, manifestPrivate, err := ed25519.GenerateKey(random)
	if err != nil {
		return Result{}, fmt.Errorf("generate run manifest signing key: %w", err)
	}
	manifestSeed := append([]byte(nil), manifestPrivate.Seed()...)
	clear(manifestPrivate)
	defer clear(manifestSeed)
	if allZero(manifestSeed) {
		return Result{}, errors.New("random source produced an all-zero run manifest signing seed")
	}

	finalExecDigest, finalExecSize, err := runtimelock.HashFile(config.Document.Runtime.HarnessFinalExecBinary)
	if err != nil {
		return Result{}, fmt.Errorf("hash harness-final-exec binary: %w", err)
	}
	paths := newOutputPaths(outputDirectory)
	files, err := renderOutputFiles(
		config, paths, pki, capabilityKey, browserBearerEntropy, cursorKey, manifestSeed,
		manifestPublic, finalExecDigest, finalExecSize, now.UTC(),
	)
	if err != nil {
		return Result{}, err
	}

	if err := os.Mkdir(outputDirectory, 0o700); err != nil {
		return Result{}, fmt.Errorf("create new insecure development output directory: %w", err)
	}
	createdOutput := true
	defer func() {
		if returnErr != nil && createdOutput {
			returnErr = errors.Join(returnErr, removeFailedOutput(outputDirectory))
		}
	}()
	if err := os.Chmod(outputDirectory, 0o700); err != nil {
		return Result{}, fmt.Errorf("restrict insecure development output directory: %w", err)
	}
	for _, relative := range outputDirectories {
		path := filepath.Join(outputDirectory, filepath.FromSlash(relative))
		if err := os.Mkdir(path, 0o700); err != nil {
			return Result{}, fmt.Errorf("create generated directory %s: %w", relative, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return Result{}, fmt.Errorf("restrict generated directory %s: %w", relative, err)
		}
	}
	fileNames := make([]string, 0, len(files))
	for relative := range files {
		fileNames = append(fileNames, relative)
	}
	sort.Strings(fileNames)
	for _, relative := range fileNames {
		if err := writeExclusiveFile(filepath.Join(outputDirectory, filepath.FromSlash(relative)), files[relative], 0o600); err != nil {
			return Result{}, fmt.Errorf("write generated file %s: %w", relative, err)
		}
	}
	createdOutput = false
	return Result{
		OutputDirectory:      outputDirectory,
		MetadataFile:         paths.metadata,
		BootstrapConfigFile:  paths.bootstrap,
		WorkerDeploymentFile: paths.workerDeployment,
		AgentxLaunchFile:     paths.agentxLaunch,
		FixturesConfigFile:   paths.fixturesConfig,
		BrowserBearerFile:    paths.browserBearer,
		EnvironmentFiles: map[string]string{
			"agentserver-core": paths.coreEnvironment,
			"browser-gateway":  paths.browserEnvironment,
			"executor-gateway": paths.executorEnvironment,
			"harness-pool":     paths.harnessPoolEnvironment,
		},
	}, nil
}

type outputPaths struct {
	root                   string
	metadata               string
	bootstrap              string
	workerDeployment       string
	keyring                string
	agentxLaunch           string
	fixturesConfig         string
	ca                     string
	capabilityKey          string
	cursorKey              string
	manifestSeed           string
	browserBearer          string
	objects                string
	harnessRuntime         string
	checkpointStaging      string
	agentxRuntime          string
	coreEnvironment        string
	browserEnvironment     string
	executorEnvironment    string
	harnessPoolEnvironment string
	certificates           map[string]string
	privateKeys            map[string]string
}

func newOutputPaths(root string) outputPaths {
	absolute := func(relative string) string { return filepath.Join(root, filepath.FromSlash(relative)) }
	paths := outputPaths{
		root: root, metadata: absolute(metadataFile), bootstrap: absolute(bootstrapConfigFile),
		workerDeployment: absolute(workerDeploymentConfigFile), keyring: absolute(manifestVerificationKeyringFile),
		agentxLaunch: absolute(agentxLaunchFile), fixturesConfig: absolute(developmentFixturesConfigFile), ca: absolute(certificateAuthorityFile),
		capabilityKey: absolute(capabilityKeyFile), cursorKey: absolute(cursorKeyFile), manifestSeed: absolute(manifestSeedFile),
		browserBearer: absolute(browserBearerTokenFile),
		objects:       absolute(objectDirectory), harnessRuntime: absolute(harnessRuntimeDirectory),
		checkpointStaging: absolute(checkpointStagingDirectory), agentxRuntime: absolute(agentxRuntimeDirectory),
		coreEnvironment: absolute(coreEnvironmentFile), browserEnvironment: absolute(browserEnvironmentFile),
		executorEnvironment: absolute(executorEnvironmentFile), harnessPoolEnvironment: absolute(harnessPoolEnvironmentFile),
		certificates: make(map[string]string, len(developmentServices)), privateKeys: make(map[string]string, len(developmentServices)),
	}
	for _, service := range developmentServices {
		paths.certificates[service] = absolute("pki/" + service + ".crt")
		paths.privateKeys[service] = absolute("pki/" + service + ".key")
	}
	return paths
}

func renderOutputFiles(
	config LoadedConfig,
	paths outputPaths,
	pki developmentPKI,
	capabilityKey, browserBearerEntropy, cursorKey, manifestSeed []byte,
	manifestPublic ed25519.PublicKey,
	finalExecDigest string,
	finalExecSize int64,
	now time.Time,
) (map[string][]byte, error) {
	files := make(map[string][]byte)
	put := func(relative string, contents []byte) { files[relative] = append([]byte(nil), contents...) }
	put(certificateAuthorityFile, pki.caPEM)
	for _, service := range developmentServices {
		identity, found := pki.identities[service]
		if !found {
			return nil, fmt.Errorf("generated PKI omitted %s", service)
		}
		put("pki/"+service+".crt", identity.certificatePEM)
		put("pki/"+service+".key", identity.privateKeyPEM)
	}
	capabilityEncoded := base64.RawURLEncoding.EncodeToString(capabilityKey)
	browserBearer := "asv2dev-browser-" + base64.RawURLEncoding.EncodeToString(browserBearerEntropy)
	cursorEncoded := base64.RawURLEncoding.EncodeToString(cursorKey)
	put(capabilityKeyFile, []byte(capabilityEncoded+"\n"))
	put(browserBearerTokenFile, []byte(browserBearer+"\n"))
	put(cursorKeyFile, []byte(cursorEncoded+"\n"))
	put(manifestSeedFile, manifestSeed)

	keyring := runmanifest.VerificationKeyringDocument{
		Version: runmanifest.VerificationKeyringVersion,
		Keys: []runmanifest.VerificationKeyDocument{{
			KeyID: manifestSigningKeyID, Algorithm: runmanifest.SignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(manifestPublic),
		}},
	}
	keyringBytes, err := marshalJSON(keyring)
	if err != nil {
		return nil, err
	}
	put(manifestVerificationKeyringFile, keyringBytes)

	authority := config.Document.Authority
	bootstrap := coreBootstrapDocument{
		Version: CurrentConfigVersion, WorkspaceID: authority.WorkspaceID, SessionID: authority.SessionID, ActorID: authority.ActorID,
		Executor: coreBootstrapExecutorDocument{
			ExecutorID: authority.ExecutorID, EnvironmentID: authority.EnvironmentID, AgentxVersion: authority.AgentxVersion,
			Platform: config.Platform, RuntimeManifestFile: config.Document.Runtime.ManifestFile,
			WorkspaceRoot: authority.WorkspaceRoot, DisplayName: authority.DisplayName,
			Description: authority.Description, DefaultCWD: authority.DefaultCWD,
		},
	}
	bootstrapBytes, err := marshalJSON(bootstrap)
	if err != nil {
		return nil, err
	}
	put(bootstrapConfigFile, bootstrapBytes)

	identities := config.Document.Identities
	worker := workerDeploymentDocument{
		Version: CurrentConfigVersion, RunManifestKeyringFile: paths.keyring,
		RuntimeManifestFile: config.Document.Runtime.ManifestFile, RuntimeBundleRoot: config.Document.Runtime.BundleRoot,
		FinalExec:          workerArtifactDocument{Path: config.Document.Runtime.HarnessFinalExecBinary, SHA256: finalExecDigest, SizeBytes: finalExecSize},
		CodexConfigProfile: harnessworker.CodexConfigProfileStable0146,
		WorkerUID:          identities.WorkerUID, WorkerGID: identities.WorkerGID, AppUID: identities.AppUID, AppGID: identities.AppGID,
		TLS: workerTLSDocument{
			CAFile: paths.ca, CertificateFile: paths.certificates["harness-worker"], KeyFile: paths.privateKeys["harness-worker"],
		},
	}
	workerBytes, err := marshalJSON(worker)
	if err != nil {
		return nil, err
	}
	put(workerDeploymentConfigFile, workerBytes)

	agentx := agentxLaunchDocument{
		Version: CurrentConfigVersion, Mode: "insecure-dev", Program: config.Document.Runtime.AgentxBinary,
		Arguments: []string{
			"connect", "--insecure-dev",
			"--gateway-url=" + strings.Replace(config.ExecutorOrigin, "https://", "wss://", 1) + "/internal/v2/agentx/connect",
			"--gateway-ca-file=" + paths.ca,
			"--executor-id=" + authority.ExecutorID,
			"--environment-id=" + authority.EnvironmentID,
			"--runtime-manifest=" + config.Document.Runtime.ManifestFile,
			"--runtime-root=" + config.Document.Runtime.BundleRoot,
			"--runtime-dir=" + paths.agentxRuntime,
			"--workspace-root=" + authority.WorkspaceRoot,
		},
	}
	agentxBytes, err := marshalJSON(agentx)
	if err != nil {
		return nil, err
	}
	put(agentxLaunchFile, agentxBytes)

	fixtures := devfixtures.ConfigDocument{
		Version: devfixtures.CurrentConfigVersion,
		Authority: devfixtures.AuthorityDocument{
			WorkspaceID: authority.WorkspaceID, SessionID: authority.SessionID, ActorID: authority.ActorID,
		},
		Hydra: devfixtures.HydraDocument{
			IntrospectionEndpoint:  config.Document.Network.HydraIntrospectionURL,
			BrowserBearerTokenFile: paths.browserBearer,
			Audience:               devfixtures.BrowserTokenAudience, Scope: devfixtures.BrowserTokenScope, ResponseTTL: "15m",
		},
		LLMProxy: devfixtures.LLMProxyDocument{
			Endpoint:        config.Document.Network.LLMProxyEndpoint,
			CertificateFile: paths.certificates["llmproxy"], PrivateKeyFile: paths.privateKeys["llmproxy"],
			RunCapabilityKeyFile: paths.capabilityKey,
			Model:                config.Document.Model.Name, Provider: config.Document.Model.Provider,
			ToolNamespace: devfixtures.ToolNamespace, ScriptedTool: devfixtures.ScriptedToolName,
			FinalMessage: "Agentserver v2 scripted development turn completed.",
		},
	}
	fixturesBytes, err := marshalJSON(fixtures)
	if err != nil {
		return nil, err
	}
	put(developmentFixturesConfigFile, fixturesBytes)

	environments, err := renderServiceEnvironments(config, paths, pki, capabilityEncoded, cursorEncoded)
	if err != nil {
		return nil, err
	}
	for relative, environment := range environments {
		raw, err := renderEnvironmentFile(environment)
		if err != nil {
			return nil, err
		}
		put(relative, raw)
	}

	tlsIdentities := make(map[string]string, len(pki.identities))
	for service, identity := range pki.identities {
		tlsIdentities[service] = identity.spiffeID
	}
	metadata := metadataDocument{
		Version: CurrentConfigVersion, Mode: "insecure-dev", CreatedAt: now.Format(time.RFC3339),
		Authority: metadataAuthorityDocument{
			WorkspaceID: authority.WorkspaceID, SessionID: authority.SessionID, ActorID: authority.ActorID,
			ExecutorID: authority.ExecutorID, EnvironmentID: authority.EnvironmentID,
		},
		Runtime: metadataRuntimeDocument{
			Platform: config.Platform, CodexRelease: config.Manifest.CodexRelease,
			RuntimeManifestSHA256:      config.ManifestSHA256,
			CheckpointAllowlistVersion: config.Manifest.CheckpointAllowlistVersion,
		},
		Endpoints: metadataEndpointDocument{
			Core: config.CoreOrigin, BrowserGateway: config.BrowserOrigin,
			AGUIEndpointTemplate: config.BrowserOrigin + "/v2/workspaces/{workspaceId}/sessions/{sessionId}/agui",
			ExecutorGateway:      config.ExecutorOrigin, ExecutorMCP: config.ExecutorOrigin + "/mcp",
			AgentxGateway: strings.Replace(config.ExecutorOrigin, "https://", "wss://", 1) + "/internal/v2/agentx/connect",
			HarnessPool:   config.HarnessPoolOrigin, HydraIntrospection: config.Document.Network.HydraIntrospectionURL,
			LLMProxy: config.Document.Network.LLMProxyEndpoint,
		},
		Files: metadataFilesDocument{
			CertificateAuthority: paths.ca, BootstrapConfig: paths.bootstrap, WorkerDeployment: paths.workerDeployment,
			AgentxLaunch: paths.agentxLaunch,
			Environment: map[string]string{
				"agentserver-core": paths.coreEnvironment, "browser-gateway": paths.browserEnvironment,
				"executor-gateway": paths.executorEnvironment, "harness-pool": paths.harnessPoolEnvironment,
			},
			LLMProxyCertificate: paths.certificates["llmproxy"], LLMProxyPrivateKey: paths.privateKeys["llmproxy"],
			RunCapabilityKey: paths.capabilityKey, DevelopmentFixtures: paths.fixturesConfig,
			BrowserBearerToken: paths.browserBearer,
		},
		TLSIdentities: tlsIdentities,
		Warnings: []string{
			"INSECURE DEVELOPMENT MATERIAL: shared HMAC keys and plaintext local state are not production credentials or storage.",
			"A real model turn requires a Linux privileged harness runtime with the fixed worker/app identities and network policy described by the v2 implementation document.",
			"The generated development CA must be trusted by the stock app-server process before it can call the generated llmproxy TLS endpoint; the worker child environment deliberately has no ambient CA override.",
		},
	}
	metadataBytes, err := marshalJSON(metadata)
	if err != nil {
		return nil, err
	}
	put(metadataFile, metadataBytes)
	return files, nil
}

func renderServiceEnvironments(
	config LoadedConfig,
	paths outputPaths,
	pki developmentPKI,
	capabilityKey, cursorKey string,
) (map[string]map[string]string, error) {
	identity := func(service string) (developmentTLSIdentity, error) {
		value, found := pki.identities[service]
		if !found {
			return developmentTLSIdentity{}, fmt.Errorf("generated PKI omitted %s", service)
		}
		return value, nil
	}
	browser, err := identity("browser-gateway")
	if err != nil {
		return nil, err
	}
	executor, err := identity("executor-gateway")
	if err != nil {
		return nil, err
	}
	pool, err := identity("harness-pool")
	if err != nil {
		return nil, err
	}
	worker, err := identity("harness-worker")
	if err != nil {
		return nil, err
	}
	llmproxy, err := identity("llmproxy")
	if err != nil {
		return nil, err
	}
	document := config.Document
	return map[string]map[string]string{
		coreEnvironmentFile: {
			"AGENTSERVER_V2_DATABASE_URL":               document.DatabaseURL,
			"AGENTSERVER_V2_CORE_LISTEN_ADDR":           document.Network.CoreListenAddress,
			"AGENTSERVER_V2_CORE_TLS_CERT_FILE":         paths.certificates["agentserver-core"],
			"AGENTSERVER_V2_CORE_TLS_KEY_FILE":          paths.privateKeys["agentserver-core"],
			"AGENTSERVER_V2_CORE_CLIENT_CA_FILE":        paths.ca,
			"AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID": executor.spiffeID,
			"AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID":     pool.spiffeID,
			"AGENTSERVER_V2_BROWSER_GATEWAY_SPIFFE_ID":  browser.spiffeID,
			"AGENTSERVER_V2_HYDRA_INTROSPECTION_URL":    document.Network.HydraIntrospectionURL,
			"AGENTSERVER_V2_HYDRA_ALLOW_INSECURE_HTTP":  "true",
			"AGENTSERVER_V2_RUN_CURSOR_KEY":             cursorKey,
			"AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR":      paths.objects,
			"AGENTSERVER_V2_RUN_POLICY_VERSION":         document.Policy.Version,
			"AGENTSERVER_V2_RUN_ALLOWED_TOOLS":          strings.Join(document.Policy.AllowedTools, ","),
		},
		browserEnvironmentFile: {
			"AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR":   document.Network.BrowserGatewayListenAddress,
			"AGENTSERVER_V2_BROWSER_GATEWAY_TLS_CERT_FILE": paths.certificates["browser-gateway"],
			"AGENTSERVER_V2_BROWSER_GATEWAY_TLS_KEY_FILE":  paths.privateKeys["browser-gateway"],
			"AGENTSERVER_V2_CORE_URL":                      config.CoreOrigin,
			"AGENTSERVER_V2_CORE_CA_FILE":                  paths.ca,
			"AGENTSERVER_V2_CORE_CLIENT_CERT_FILE":         paths.certificates["browser-gateway"],
			"AGENTSERVER_V2_CORE_CLIENT_KEY_FILE":          paths.privateKeys["browser-gateway"],
		},
		executorEnvironmentFile: {
			"AGENTSERVER_V2_EXECUTOR_GATEWAY_LISTEN_ADDR":   document.Network.ExecutorGatewayListenAddress,
			"AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_CERT_FILE": paths.certificates["executor-gateway"],
			"AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_KEY_FILE":  paths.privateKeys["executor-gateway"],
			"AGENTSERVER_V2_CORE_URL":                       config.CoreOrigin,
			"AGENTSERVER_V2_CORE_CA_FILE":                   paths.ca,
			"AGENTSERVER_V2_CORE_CLIENT_CERT_FILE":          paths.certificates["executor-gateway"],
			"AGENTSERVER_V2_CORE_CLIENT_KEY_FILE":           paths.privateKeys["executor-gateway"],
			"AGENTSERVER_V2_DEV_EXECUTOR_ID":                document.Authority.ExecutorID,
			"AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY":         capabilityKey,
			"AGENTSERVER_V2_EXECUTION_POLICY_VERSION":       document.Policy.Version,
			"AGENTSERVER_V2_SHELL_POLICY_DECISION":          "ask",
			"AGENTSERVER_V2_READ_FILE_POLICY_DECISION":      "allow",
		},
		harnessPoolEnvironmentFile: {
			"AGENTSERVER_V2_HARNESS_POOL_LISTEN_ADDR":        document.Network.HarnessPoolListenAddress,
			"AGENTSERVER_V2_HARNESS_POOL_TLS_CERT_FILE":      paths.certificates["harness-pool"],
			"AGENTSERVER_V2_HARNESS_POOL_TLS_KEY_FILE":       paths.privateKeys["harness-pool"],
			"AGENTSERVER_V2_HARNESS_POOL_WORKER_CA_FILE":     paths.ca,
			"AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID":          pool.spiffeID,
			"AGENTSERVER_V2_HARNESS_WORKER_SPIFFE_ID":        worker.spiffeID,
			"AGENTSERVER_V2_CORE_URL":                        config.CoreOrigin,
			"AGENTSERVER_V2_CORE_CA_FILE":                    paths.ca,
			"AGENTSERVER_V2_DEV_EXECUTOR_ID":                 document.Authority.ExecutorID,
			"AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY":          capabilityKey,
			"AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR":           paths.objects,
			"AGENTSERVER_V2_HARNESS_RUNTIME_DIR":             paths.harnessRuntime,
			"AGENTSERVER_V2_CHECKPOINT_STAGING_DIR":          paths.checkpointStaging,
			"AGENTSERVER_V2_HARNESS_WORKER_BIN":              document.Runtime.HarnessWorkerBinary,
			"AGENTSERVER_V2_HARNESS_WORKER_CONFIG_FILE":      paths.workerDeployment,
			"AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_ID":     manifestSigningKeyID,
			"AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_FILE":   paths.manifestSeed,
			"AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256":   config.ManifestSHA256,
			"AGENTSERVER_V2_CHECKPOINT_ALLOWLIST_VERSION":    strconv.Itoa(config.Manifest.CheckpointAllowlistVersion),
			"AGENTSERVER_V2_HARNESS_WORKER_SERVICE_ACCOUNT":  workerServiceAccount,
			"AGENTSERVER_V2_HARNESS_PRIVILEGED_FORK":         "true",
			"AGENTSERVER_V2_HARNESS_WORKER_UID":              strconv.FormatUint(uint64(document.Identities.WorkerUID), 10),
			"AGENTSERVER_V2_HARNESS_WORKER_GID":              strconv.FormatUint(uint64(document.Identities.WorkerGID), 10),
			"AGENTSERVER_V2_HARNESS_APP_UID":                 strconv.FormatUint(uint64(document.Identities.AppUID), 10),
			"AGENTSERVER_V2_HARNESS_APP_GID":                 strconv.FormatUint(uint64(document.Identities.AppGID), 10),
			"AGENTSERVER_V2_EXECUTOR_MCP_ENDPOINT":           config.ExecutorOrigin + "/mcp",
			"AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID":      executor.spiffeID,
			"AGENTSERVER_V2_MODEL":                           document.Model.Name,
			"AGENTSERVER_V2_MODEL_PROVIDER":                  document.Model.Provider,
			"AGENTSERVER_V2_LLMPROXY_ENDPOINT":               document.Network.LLMProxyEndpoint,
			"AGENTSERVER_V2_LLMPROXY_SPIFFE_ID":              llmproxy.spiffeID,
			"AGENTSERVER_V2_HARNESS_MAX_CONCURRENT_ATTEMPTS": strconv.Itoa(document.Harness.MaxConcurrentAttempts),
			"AGENTSERVER_V2_MAX_RUN_DURATION":                document.Harness.MaxRunDuration,
		},
	}, nil
}

func validateOutputDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("output directory must be an absolute clean path")
	}
	if err := validateShellValue("output directory", path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("output directory already exists; prepare never overwrites or merges development authority")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	parent := filepath.Dir(path)
	if err := validateExistingCanonicalDirectory("output directory parent", parent); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("output directory parent must not be writable by group or other")
	}
	return nil
}

func randomSecret(random io.Reader, size int, label string) ([]byte, error) {
	secret := make([]byte, size)
	if _, err := io.ReadFull(random, secret); err != nil {
		clear(secret)
		return nil, fmt.Errorf("generate %s: %w", label, err)
	}
	if allZero(secret) {
		clear(secret)
		return nil, fmt.Errorf("random source produced an all-zero %s", label)
	}
	return secret, nil
}

func allZero(value []byte) bool {
	return bytes.Equal(value, make([]byte, len(value)))
}

func marshalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeExclusiveFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func removeFailedOutput(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("refuse to clean an invalid failed output path")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clean incomplete generated output: %w", err)
	}
	return nil
}
