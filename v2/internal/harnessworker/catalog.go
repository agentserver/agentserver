// Package harnessworker contains the deterministic protocol core used by the
// per-run harness worker. It does not run a model or execute local tools.
package harnessworker

import "github.com/agentserver/agentserver/v2/internal/braincatalog"

const CatalogCanonicalizer = braincatalog.CatalogCanonicalizer

const (
	maxConfiguredTools            = 4 * 1024
	maxConfiguredNameBytes        = 1024
	maxConfiguredDescriptionBytes = 1024 * 1024
	maxConfiguredSchemaBytes      = 16 * 1024 * 1024
	maxConfiguredPayloadBytes     = 64 * 1024 * 1024
	maxConfiguredResultItems      = 4 * 1024
	maxConfiguredJSONValues       = 1024 * 1024
	maxConfiguredJSONDepth        = 256
)

type Limits braincatalog.Limits

func DefaultLimits() Limits {
	return Limits(braincatalog.DefaultLimits())
}

func (limits Limits) validate() error {
	return braincatalog.Limits(limits).Validate()
}

type ToolDescriptor = braincatalog.ToolDescriptor
type CatalogTool = braincatalog.CatalogTool
type Catalog = braincatalog.Catalog
type DynamicNamespace = braincatalog.DynamicNamespace
type DynamicFunction = braincatalog.DynamicFunction

func BuildCatalog(namespace, namespaceDescription string, descriptors []ToolDescriptor, limits Limits) (*Catalog, error) {
	return braincatalog.BuildCatalog(namespace, namespaceDescription, descriptors, braincatalog.Limits(limits))
}

func equalDigest(left, right string) bool {
	return braincatalog.EqualDigest(left, right)
}

func decodeCanonicalJSON(raw []byte, maxBytes int, limits Limits) (any, []byte, error) {
	return braincatalog.DecodeCanonicalJSON(raw, maxBytes, braincatalog.Limits(limits))
}

func validateNamespace(name string, maxBytes int) error {
	return braincatalog.ValidateNamespace(name, maxBytes)
}

func validateNameText(label, value string, maxBytes int) error {
	return braincatalog.ValidateNameText(label, value, maxBytes)
}

func validateText(label, value string, maxBytes int) error {
	return braincatalog.ValidateText(label, value, maxBytes)
}
