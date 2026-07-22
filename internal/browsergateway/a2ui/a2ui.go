// Package a2ui hand-builds A2UI v0.9 generative-UI payloads
// (https://github.com/a2ui-project/a2ui) for gateway-synthesized cards.
// There is no Go producer SDK; these structs mirror the v0.9 JSON Schema.
// Payloads are delivered over AG-UI as a CUSTOM event {name:"a2ui.operations",
// value:[]Message}. Component model: flat adjacency list, one id:"root";
// single-child containers use "child" (a component id), multi-child use
// "children" (ids).
package a2ui

const (
	// Version is the A2UI wire version this package emits.
	Version = "v0.9"
	// CatalogID is the basic component catalog for v0.9.
	CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// Message is one A2UI server->client message: exactly one payload key set.
type Message struct {
	Version          string            `json:"version"`
	CreateSurface    *CreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *UpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *UpdateDataModel  `json:"updateDataModel,omitempty"`
}

type CreateSurface struct {
	SurfaceID     string `json:"surfaceId"`
	CatalogID     string `json:"catalogId"`
	SendDataModel bool   `json:"sendDataModel,omitempty"`
}

type UpdateComponents struct {
	SurfaceID  string      `json:"surfaceId"`
	Components []Component `json:"components"`
}

type UpdateDataModel struct {
	SurfaceID string `json:"surfaceId"`
	Value     any    `json:"value,omitempty"`
}

// Component is one node in the flat adjacency list. Only the fields used by
// this package's cards are modeled; A2UI ignores unknown props on render.
type Component struct {
	ID        string   `json:"id"`
	Component string   `json:"component"`          // "Card" | "Column" | "Text"
	Child     string   `json:"child,omitempty"`    // single-child containers (Card)
	Children  []string `json:"children,omitempty"` // multi-child containers (Column)
	Text      any      `json:"text,omitempty"`     // literal string OR Binding
}

// Binding is a data-model reference: {"path":"/ptr"} (RFC 6901 JSON Pointer).
type Binding struct {
	Path string `json:"path"`
}

// bind is a small helper for a data-model binding.
func bind(path string) Binding { return Binding{Path: path} }
