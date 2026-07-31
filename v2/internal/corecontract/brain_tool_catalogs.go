package corecontract

import (
	"encoding/json"
	"time"
)

const (
	FreezeBrainToolCatalogPath = "/internal/v2/brain-tool-catalogs:freeze"
	BrainToolCatalogPathPrefix = "/internal/v2/brain-tool-catalogs/"
)

type BrainToolCatalogState struct {
	CatalogID                string          `json:"catalogId"`
	WorkspaceID              string          `json:"workspaceId"`
	SessionID                string          `json:"sessionId"`
	CreatedRunID             string          `json:"createdRunId"`
	CreatedRunAttemptID      string          `json:"createdRunAttemptId"`
	CreatedAttemptGeneration int64           `json:"createdAttemptGeneration"`
	CreatedHolderID          string          `json:"createdHolderId"`
	CreatedRunVersion        int64           `json:"createdRunVersion"`
	CreatedAttemptVersion    int64           `json:"createdAttemptVersion"`
	ThreadID                 string          `json:"threadId,omitempty"`
	ContractVersion          string          `json:"contractVersion"`
	CanonicalizerVersion     string          `json:"canonicalizerVersion"`
	CanonicalCatalog         json.RawMessage `json:"canonicalCatalog"`
	CatalogDigest            string          `json:"catalogDigest"`
	PolicyVersion            string          `json:"policyVersion"`
	PolicyContextDigest      string          `json:"policyContextDigest"`
	Version                  int64           `json:"version"`
	CreatedAt                time.Time       `json:"createdAt"`
	UpdatedAt                time.Time       `json:"updatedAt"`
}

type FreezeBrainToolCatalogRequest struct {
	CatalogID                 string          `json:"catalogId"`
	WorkspaceID               string          `json:"workspaceId"`
	SessionID                 string          `json:"sessionId"`
	RunID                     string          `json:"runId"`
	RunAttemptID              string          `json:"runAttemptId"`
	HolderID                  string          `json:"holderId"`
	RunAttemptGeneration      int64           `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64           `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64           `json:"expectedRunAttemptVersion"`
	ContractVersion           string          `json:"contractVersion"`
	CanonicalizerVersion      string          `json:"canonicalizerVersion"`
	CanonicalCatalog          json.RawMessage `json:"canonicalCatalog"`
	CatalogDigest             string          `json:"catalogDigest"`
	PolicyVersion             string          `json:"policyVersion"`
	PolicyContextDigest       string          `json:"policyContextDigest"`
}

type FreezeBrainToolCatalogResponse struct {
	Catalog BrainToolCatalogState `json:"catalog"`
	Created bool                  `json:"created"`
}

type BindBrainThreadCatalogRequest struct {
	CatalogID                 string `json:"catalogId"`
	RunID                     string `json:"runId"`
	RunAttemptID              string `json:"runAttemptId"`
	HolderID                  string `json:"holderId"`
	RunAttemptGeneration      int64  `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64  `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64  `json:"expectedRunAttemptVersion"`
	ExpectedCatalogVersion    int64  `json:"expectedCatalogVersion"`
	ThreadID                  string `json:"threadId"`
}

type BindBrainThreadCatalogResponse struct {
	Catalog BrainToolCatalogState `json:"catalog"`
	Changed bool                  `json:"changed"`
}

func BindBrainThreadCatalogPath(catalogID string) string {
	return BrainToolCatalogPathPrefix + catalogID + ":bindThread"
}
