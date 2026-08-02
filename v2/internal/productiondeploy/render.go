package productiondeploy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	foundationFile = "00-foundation.json"
	migrationFile  = "10-migrate.json"
	bootstrapFile  = "20-bootstrap.json"
	runtimeFile    = "30-runtime.json"
	checksumsFile  = "checksums.json"
)

type RenderedFile struct {
	Name    string
	Content []byte
	SHA256  string
}

type Bundle struct {
	Files []RenderedFile
}

func (bundle Bundle) File(name string) ([]byte, bool) {
	for _, file := range bundle.Files {
		if file.Name == name {
			return append([]byte(nil), file.Content...), true
		}
	}
	return nil, false
}

func Render(config LoadedConfig) (Bundle, error) {
	validated, err := ValidateConfig(config.Document)
	if err != nil {
		return Bundle{}, fmt.Errorf("validate production render input: %w", err)
	}
	config = validated

	bootstrapJSON, err := renderBootstrapJSON(config)
	if err != nil {
		return Bundle{}, err
	}
	workerJSON, err := renderWorkerDeploymentJSON(config)
	if err != nil {
		return Bundle{}, err
	}
	networkGuardJSON, err := renderHarnessNetworkGuardJSON(config)
	if err != nil {
		return Bundle{}, err
	}
	documentJSON, err := json.Marshal(config.Document)
	if err != nil {
		return Bundle{}, fmt.Errorf("encode validated production deployment: %w", err)
	}
	bootstrapHash := sha256Hex(bootstrapJSON)
	harnessConfigHash := sha256Framed(workerJSON, networkGuardJSON)
	harnessDeploymentHash := sha256Framed(documentJSON, workerJSON, networkGuardJSON)
	documentHash := sha256Hex(documentJSON)

	catalog, err := coredb.EmbeddedMigrations()
	if err != nil || len(catalog) == 0 {
		return Bundle{}, errors.New("load embedded migration catalog for production deployment")
	}
	migrationVersion := catalog[len(catalog)-1].Version

	context := renderContext{
		config: config, bootstrapJSON: bootstrapJSON, workerJSON: workerJSON,
		networkGuardJSON: networkGuardJSON, bootstrapHash: bootstrapHash,
		harnessConfigHash: harnessConfigHash, harnessDeploymentHash: harnessDeploymentHash,
		documentHash: documentHash, migrationVersion: migrationVersion,
		bootstrapConfigName: "agentserver-bootstrap-" + bootstrapHash[:12],
		harnessConfigName:   "agentserver-harness-" + harnessConfigHash[:12],
		migrationJobName:    fmt.Sprintf("agentserver-migrate-v%04d", migrationVersion),
		bootstrapJobName:    "agentserver-bootstrap-" + bootstrapHash[:12],
	}

	runtimeItems, err := renderRuntime(context)
	if err != nil {
		return Bundle{}, err
	}
	groups := []struct {
		name  string
		items []kubeObject
	}{
		{name: foundationFile, items: renderFoundation(context)},
		{name: migrationFile, items: []kubeObject{renderMigrationJob(context)}},
		{name: bootstrapFile, items: []kubeObject{renderBootstrapJob(context)}},
		{name: runtimeFile, items: runtimeItems},
	}
	files := make([]RenderedFile, 0, len(groups)+1)
	for _, group := range groups {
		content, err := marshalKubernetesList(group.items)
		if err != nil {
			return Bundle{}, fmt.Errorf("render %s: %w", group.name, err)
		}
		files = append(files, RenderedFile{Name: group.name, Content: content, SHA256: sha256Hex(content)})
	}
	checksumContent, err := renderChecksums(files)
	if err != nil {
		return Bundle{}, err
	}
	files = append(files, RenderedFile{Name: checksumsFile, Content: checksumContent, SHA256: sha256Hex(checksumContent)})
	return Bundle{Files: files}, nil
}

type renderContext struct {
	config                LoadedConfig
	bootstrapJSON         []byte
	workerJSON            []byte
	networkGuardJSON      []byte
	bootstrapHash         string
	harnessConfigHash     string
	harnessDeploymentHash string
	documentHash          string
	migrationVersion      int64
	bootstrapConfigName   string
	harnessConfigName     string
	migrationJobName      string
	bootstrapJobName      string
}

type productionBootstrapJSON struct {
	Version     int                             `json:"version"`
	WorkspaceID string                          `json:"workspaceId"`
	SessionID   string                          `json:"sessionId"`
	UserID      string                          `json:"userId"`
	Identity    productionBootstrapIdentityJSON `json:"identity"`
	ExecutorID  string                          `json:"executorId"`
}

type productionBootstrapIdentityJSON struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

func renderBootstrapJSON(config LoadedConfig) ([]byte, error) {
	document := config.Document
	return marshalCanonicalDocument(productionBootstrapJSON{
		Version: 1, WorkspaceID: document.Bootstrap.WorkspaceID,
		SessionID: document.Bootstrap.SessionID, UserID: document.Bootstrap.OwnerUserID,
		Identity: productionBootstrapIdentityJSON{
			Issuer: document.OAuth.ExternalOIDC.Issuer, Subject: document.Bootstrap.ExternalOIDCSubject,
		},
		ExecutorID: document.Bootstrap.ExecutorID,
	})
}

type workerDeploymentJSON struct {
	Version                int                `json:"version"`
	RunManifestKeyringFile string             `json:"runManifestKeyringFile"`
	RuntimeManifestFile    string             `json:"runtimeManifestFile"`
	RuntimeBundleRoot      string             `json:"runtimeBundleRoot"`
	FinalExec              workerArtifactJSON `json:"finalExec"`
	CodexConfigProfile     string             `json:"codexConfigProfile"`
	WorkerUID              uint32             `json:"workerUid"`
	WorkerGID              uint32             `json:"workerGid"`
	AppUID                 uint32             `json:"appUid"`
	AppGID                 uint32             `json:"appGid"`
	TLS                    workerTLSJSON      `json:"tls"`
}

type workerArtifactJSON struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type workerTLSJSON struct {
	CAFile          string `json:"caFile"`
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

func renderWorkerDeploymentJSON(config LoadedConfig) ([]byte, error) {
	return marshalCanonicalDocument(workerDeploymentJSON{
		Version:                1,
		RunManifestKeyringFile: workerMaterialPath("run-manifest-keyring.json"),
		RuntimeManifestFile:    "/opt/agentserver/runtime/runtime-manifest.json",
		RuntimeBundleRoot:      "/opt/agentserver/runtime/bundle",
		FinalExec: workerArtifactJSON{
			Path: "/usr/local/bin/harness-final-exec", SHA256: config.Document.Runtime.FinalExecSHA256,
			SizeBytes: config.Document.Runtime.FinalExecSizeBytes,
		},
		CodexConfigProfile: CodexConfigProfile(),
		WorkerUID:          WorkerUID, WorkerGID: WorkerGID, AppUID: AppUID, AppGID: AppGID,
		TLS: workerTLSJSON{
			CAFile: workerMaterialPath("ca.crt"), CertificateFile: workerMaterialPath("tls.crt"),
			KeyFile: workerMaterialPath("tls.key"),
		},
	})
}

type networkGuardJSON struct {
	Version  int                      `json:"version"`
	Table    string                   `json:"table"`
	Policies []networkGuardPolicyJSON `json:"policies"`
}

type networkGuardPolicyJSON struct {
	UID uint32                     `json:"uid"`
	TCP []networkGuardEndpointJSON `json:"tcp"`
}

type networkGuardEndpointJSON struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

func renderHarnessNetworkGuardJSON(config LoadedConfig) ([]byte, error) {
	document := config.Document
	return marshalCanonicalDocument(networkGuardJSON{
		Version: 1, Table: "agentserver_harness",
		Policies: []networkGuardPolicyJSON{
			{UID: WorkerUID, TCP: []networkGuardEndpointJSON{
				{Address: "127.0.0.1", Port: HarnessControlPort},
				{Address: document.Services.ExecutorGateway.ClusterIP, Port: document.Services.ExecutorGateway.Port},
			}},
			{UID: AppUID, TCP: []networkGuardEndpointJSON{
				{Address: document.Services.LLMProxy.ClusterIP, Port: document.Services.LLMProxy.Port},
			}},
		},
	})
}

func marshalCanonicalDocument(value any) ([]byte, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func renderChecksums(files []RenderedFile) ([]byte, error) {
	type checksumEntry struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}
	type checksumDocument struct {
		Version int             `json:"version"`
		Files   []checksumEntry `json:"files"`
	}
	entries := make([]checksumEntry, len(files))
	for index, file := range files {
		entries[index] = checksumEntry{Name: file.Name, SHA256: file.SHA256}
	}
	slices.SortFunc(entries, func(left, right checksumEntry) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	content, err := json.MarshalIndent(checksumDocument{Version: 1, Files: entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func sha256Hex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func sha256Framed(parts ...[]byte) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func serviceMaterialPath(name string) string { return "/var/run/agentserver/material/" + name }

func poolMaterialPath(name string) string { return "/var/run/agentserver/pool/" + name }

func workerMaterialPath(name string) string { return "/var/run/agentserver/worker/" + name }
