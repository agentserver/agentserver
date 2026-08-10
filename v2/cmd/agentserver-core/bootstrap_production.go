package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	productionBootstrapVersion      = 1
	maximumProductionBootstrapBytes = 64 * 1024
)

var productionBootstrapUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type productionBootstrapDocument struct {
	Version     int    `json:"version"`
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	UserID      string `json:"userId"`
	ExecutorID  string `json:"executorId"`
}

type productionBootstrapCommandResult struct {
	Bootstrap   coredb.ProductionBootstrapResult
	WorkspaceID string
	SessionID   string
	UserID      string
	ExecutorID  string
}

func bootstrapProduction(ctx context.Context, databaseURL, configPath string) (productionBootstrapCommandResult, error) {
	bootstrap, err := loadProductionBootstrap(configPath)
	if err != nil {
		return productionBootstrapCommandResult{}, err
	}
	result, err := coredb.BootstrapProduction(ctx, databaseURL, bootstrap)
	if err != nil {
		return productionBootstrapCommandResult{}, err
	}
	return productionBootstrapCommandResult{
		Bootstrap: result, WorkspaceID: bootstrap.WorkspaceID, SessionID: bootstrap.SessionID,
		UserID: bootstrap.UserID, ExecutorID: bootstrap.ExecutorID,
	}, nil
}

func loadProductionBootstrap(configPath string) (coredb.ProductionBootstrap, error) {
	raw, err := readProductionBootstrapFile(configPath)
	if err != nil {
		return coredb.ProductionBootstrap{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 64
	limits.MaxJSONDepth = 4
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumProductionBootstrapBytes, limits); err != nil {
		return coredb.ProductionBootstrap{}, fmt.Errorf("validate production bootstrap JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document productionBootstrapDocument
	if err := decoder.Decode(&document); err != nil {
		return coredb.ProductionBootstrap{}, fmt.Errorf("decode production bootstrap config: %w", err)
	}
	if err := finishProductionBootstrapJSON(decoder); err != nil {
		return coredb.ProductionBootstrap{}, fmt.Errorf("finish production bootstrap config: %w", err)
	}
	if err := validateProductionBootstrapDocument(document); err != nil {
		return coredb.ProductionBootstrap{}, err
	}
	// The external OIDC issuer and the seed owner's subject are deploy-time inputs
	// (Pulumi config -> agentserver-core-secrets -> env), not baked into the locked
	// bootstrap document, so the owner is seeded against exactly the issuer the core
	// serve process validates tokens against.
	issuer, subject, err := externalOIDCBootstrapIdentityFromEnvironment()
	if err != nil {
		return coredb.ProductionBootstrap{}, err
	}
	return coredb.ProductionBootstrap{
		WorkspaceID: document.WorkspaceID, SessionID: document.SessionID, UserID: document.UserID,
		ExternalOIDCIssuer: issuer, ExternalOIDCSubject: subject,
		ExecutorID: document.ExecutorID,
	}, nil
}

func validateProductionBootstrapDocument(document productionBootstrapDocument) error {
	if document.Version != productionBootstrapVersion {
		return fmt.Errorf("production bootstrap version must be %d", productionBootstrapVersion)
	}
	for name, value := range map[string]string{
		"workspaceId": document.WorkspaceID, "sessionId": document.SessionID,
		"userId": document.UserID, "executorId": document.ExecutorID,
	} {
		if value == "00000000-0000-0000-0000-000000000000" || !productionBootstrapUUIDPattern.MatchString(value) {
			return fmt.Errorf("production bootstrap %s must be a non-zero canonical lowercase UUID", name)
		}
	}
	return nil
}

// externalOIDCBootstrapIdentityFromEnvironment reads and validates the external
// OIDC issuer and seed-owner subject that the deploy-time environment injects
// (from the Pulumi-managed core Secret). The rules mirror the core serve issuer
// validation and the former baked bootstrap identity checks.
func externalOIDCBootstrapIdentityFromEnvironment() (string, string, error) {
	issuer := os.Getenv(coreExternalOIDCIssuerEnvironment)
	if len(issuer) < 8 || len(issuer) > 2048 ||
		strings.TrimSpace(issuer) != issuer || strings.HasSuffix(issuer, "/") {
		return "", "", fmt.Errorf("%s must be bounded canonical URL text without a trailing slash", coreExternalOIDCIssuerEnvironment)
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", "", fmt.Errorf("%s must be an absolute HTTPS URL without credentials, query, or fragment", coreExternalOIDCIssuerEnvironment)
	}
	subject := os.Getenv(coreExternalOIDCSubjectEnvironment)
	if subject == "" || len(subject) > 2048 || strings.ContainsRune(subject, 0) {
		return "", "", fmt.Errorf("%s must contain between 1 and 2048 bytes without NUL", coreExternalOIDCSubjectEnvironment)
	}
	return issuer, subject, nil
}

func readProductionBootstrapFile(filePath string) ([]byte, error) {
	if filePath == "" || !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath {
		return nil, errors.New("production bootstrap config path must be absolute and clean")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open production bootstrap config: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect production bootstrap config: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o022 != 0 || before.Size() < 1 || before.Size() > maximumProductionBootstrapBytes {
		return nil, fmt.Errorf("production bootstrap config must resolve to an immutable regular file between 1 and %d bytes", maximumProductionBootstrapBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumProductionBootstrapBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read production bootstrap config: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || int64(len(raw)) != before.Size() {
		return nil, errors.New("production bootstrap config changed while it was being read")
	}
	return raw, nil
}

func finishProductionBootstrapJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains more than one JSON value")
		}
		return err
	}
	return nil
}
