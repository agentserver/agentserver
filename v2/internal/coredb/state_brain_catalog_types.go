package coredb

import "time"

type BrainToolCatalog struct {
	ID                       string
	WorkspaceID              string
	SessionID                string
	CreatedRunID             string
	CreatedRunAttemptID      string
	CreatedAttemptGeneration int64
	CreatedHolderID          string
	CreatedRunVersion        int64
	CreatedAttemptVersion    int64
	ThreadID                 string
	ContractVersion          string
	CanonicalizerVersion     string
	CanonicalCatalog         []byte
	CatalogDigest            [32]byte
	PolicyVersion            string
	PolicyContextDigest      [32]byte
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type FreezeBrainToolCatalogCommand struct {
	CatalogID              string
	WorkspaceID            string
	SessionID              string
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	ContractVersion        string
	CanonicalizerVersion   string
	CanonicalCatalog       []byte
	CatalogDigest          [32]byte
	PolicyVersion          string
	PolicyContextDigest    [32]byte
}

type FreezeBrainToolCatalogResult struct {
	Catalog BrainToolCatalog
	Created bool
}

type BindBrainThreadCatalogCommand struct {
	CatalogID              string
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	ExpectedCatalogVersion int64
	ThreadID               string
}

type BindBrainThreadCatalogResult struct {
	Catalog BrainToolCatalog
	Changed bool
}
