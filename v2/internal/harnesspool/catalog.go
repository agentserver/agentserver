package harnesspool

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

// ExecutorCatalogPolicy is the already-authorized policy projection used to
// freeze a new brain thread's model-visible tools. An empty AllowedTools list
// deliberately freezes an empty catalog; it never means "all tools".
type ExecutorCatalogPolicy struct {
	Version       string
	ContextDigest [32]byte
	AllowedTools  []string
}

type ExecutorCatalogProposal struct {
	ContractVersion      string
	CanonicalizerVersion string
	CanonicalCatalog     []byte
	CatalogDigest        [32]byte
	PolicyVersion        string
	PolicyContextDigest  [32]byte
	Catalog              *braincatalog.Catalog
}

func BuildExecutorCatalog(policy ExecutorCatalogPolicy) (ExecutorCatalogProposal, error) {
	if len(policy.Version) < 1 || len(policy.Version) > 128 || !utf8.ValidString(policy.Version) || strings.ContainsRune(policy.Version, 0) {
		return ExecutorCatalogProposal{}, errors.New("executor catalog policy version must contain between 1 and 128 valid UTF-8 bytes without NUL")
	}
	if policy.ContextDigest == ([32]byte{}) {
		return ExecutorCatalogProposal{}, errors.New("executor catalog policy context digest is required")
	}
	allowed := make(map[string]struct{}, len(policy.AllowedTools))
	for _, name := range policy.AllowedTools {
		if name == "" {
			return ExecutorCatalogProposal{}, errors.New("executor catalog policy contains an empty tool name")
		}
		if _, duplicate := allowed[name]; duplicate {
			return ExecutorCatalogProposal{}, fmt.Errorf("executor catalog policy contains duplicate tool %q", name)
		}
		if _, found := mcpcontract.Lookup(name); !found {
			return ExecutorCatalogProposal{}, fmt.Errorf("executor catalog policy tool %q is not in contract %s", name, mcpcontract.Version)
		}
		allowed[name] = struct{}{}
	}

	descriptors := make([]braincatalog.ToolDescriptor, 0, len(allowed))
	for _, tool := range mcpcontract.Tools() {
		if _, ok := allowed[tool.Name]; !ok {
			continue
		}
		descriptors = append(descriptors, braincatalog.ToolDescriptor{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
		})
	}
	catalog, err := braincatalog.BuildCatalog(
		mcpcontract.Namespace,
		mcpcontract.NamespaceDescription,
		descriptors,
		braincatalog.DefaultLimits(),
	)
	if err != nil {
		return ExecutorCatalogProposal{}, fmt.Errorf("build executor catalog: %w", err)
	}
	return ExecutorCatalogProposal{
		ContractVersion: mcpcontract.Version, CanonicalizerVersion: braincatalog.CatalogCanonicalizer,
		CanonicalCatalog: catalog.CanonicalBytes(), CatalogDigest: catalog.DigestSHA256(),
		PolicyVersion: policy.Version, PolicyContextDigest: policy.ContextDigest, Catalog: catalog,
	}, nil
}
