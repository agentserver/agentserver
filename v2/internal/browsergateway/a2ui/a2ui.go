// Package a2ui builds the display-only A2UI v0.9 operations carried by
// browser-gateway in AG-UI CUSTOM events. It deliberately has no model or
// client-action surface.
package a2ui

import (
	"errors"
	"fmt"
	"strings"
)

const (
	Version   = "v0.9"
	CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// Message is one A2UI server-to-client operation. Exactly one operation must
// be present.
type Message struct {
	Version          string            `json:"version"`
	CreateSurface    *CreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *UpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *UpdateDataModel  `json:"updateDataModel,omitempty"`
	DeleteSurface    *DeleteSurface    `json:"deleteSurface,omitempty"`
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
	Path      string `json:"path,omitempty"`
	Value     any    `json:"value,omitempty"`
}

type DeleteSurface struct {
	SurfaceID string `json:"surfaceId"`
}

// Component models the display-only subset of the A2UI basic catalog used by
// browser-gateway: Card, Column, and Text.
type Component struct {
	ID        string   `json:"id"`
	Component string   `json:"component"`
	Child     string   `json:"child,omitempty"`
	Children  []string `json:"children,omitempty"`
	Text      any      `json:"text,omitempty"`
}

type Binding struct {
	Path string `json:"path"`
}

func bind(path string) Binding { return Binding{Path: path} }

// ValidateOperations checks the closed display-only shape and the ordering
// expected by an A2UI renderer.
func ValidateOperations(messages []Message) error {
	if len(messages) == 0 {
		return errors.New("A2UI operations must not be empty")
	}
	created := make(map[string]struct{})
	for index, message := range messages {
		if err := message.validate(); err != nil {
			return fmt.Errorf("A2UI operation %d: %w", index, err)
		}
		surfaceID, operation := message.surface()
		switch operation {
		case "create":
			if _, exists := created[surfaceID]; exists {
				return fmt.Errorf("surface %q is created more than once", surfaceID)
			}
			created[surfaceID] = struct{}{}
		case "update-components", "update-data", "delete":
			if _, exists := created[surfaceID]; !exists {
				return fmt.Errorf("surface %q is used before createSurface", surfaceID)
			}
			if operation == "delete" {
				delete(created, surfaceID)
			}
		}
	}
	return nil
}

func (message Message) validate() error {
	if message.Version != Version {
		return fmt.Errorf("version must be %q", Version)
	}
	count := 0
	for _, present := range []bool{
		message.CreateSurface != nil,
		message.UpdateComponents != nil,
		message.UpdateDataModel != nil,
		message.DeleteSurface != nil,
	} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("exactly one A2UI operation must be present")
	}
	if message.CreateSurface != nil {
		if err := validateSurfaceID(message.CreateSurface.SurfaceID); err != nil {
			return err
		}
		if message.CreateSurface.CatalogID != CatalogID {
			return fmt.Errorf("catalogId must be %q", CatalogID)
		}
		if message.CreateSurface.SendDataModel {
			return errors.New("display-only surfaces must not request data-model echo")
		}
	}
	if message.UpdateComponents != nil {
		if err := validateSurfaceID(message.UpdateComponents.SurfaceID); err != nil {
			return err
		}
		if err := validateComponents(message.UpdateComponents.Components); err != nil {
			return err
		}
	}
	if message.UpdateDataModel != nil {
		if err := validateSurfaceID(message.UpdateDataModel.SurfaceID); err != nil {
			return err
		}
		if message.UpdateDataModel.Path != "" && !strings.HasPrefix(message.UpdateDataModel.Path, "/") {
			return errors.New("updateDataModel.path must be an absolute JSON Pointer")
		}
		if message.UpdateDataModel.Value == nil {
			return errors.New("display-only updateDataModel.value is required")
		}
	}
	if message.DeleteSurface != nil {
		return validateSurfaceID(message.DeleteSurface.SurfaceID)
	}
	return nil
}

func (message Message) surface() (string, string) {
	switch {
	case message.CreateSurface != nil:
		return message.CreateSurface.SurfaceID, "create"
	case message.UpdateComponents != nil:
		return message.UpdateComponents.SurfaceID, "update-components"
	case message.UpdateDataModel != nil:
		return message.UpdateDataModel.SurfaceID, "update-data"
	default:
		return message.DeleteSurface.SurfaceID, "delete"
	}
}

func validateSurfaceID(surfaceID string) error {
	if surfaceID == "" || len(surfaceID) > 512 || strings.ContainsAny(surfaceID, "\x00\r\n") {
		return errors.New("surfaceId must be bounded text without NUL or line breaks")
	}
	return nil
}

func validateComponents(components []Component) error {
	if len(components) == 0 || len(components) > 512 {
		return errors.New("components must contain between 1 and 512 entries")
	}
	byID := make(map[string]Component, len(components))
	for _, component := range components {
		if component.ID == "" || len(component.ID) > 256 || strings.ContainsAny(component.ID, "\x00\r\n") {
			return errors.New("component id must be bounded text without NUL or line breaks")
		}
		if _, exists := byID[component.ID]; exists {
			return fmt.Errorf("component id %q is duplicated", component.ID)
		}
		byID[component.ID] = component
		switch component.Component {
		case "Card":
			if component.Child == "" || len(component.Children) != 0 || component.Text != nil {
				return fmt.Errorf("Card %q must contain only child", component.ID)
			}
		case "Column":
			if len(component.Children) == 0 || component.Child != "" || component.Text != nil {
				return fmt.Errorf("Column %q must contain only children", component.ID)
			}
		case "Text":
			if component.Text == nil || component.Child != "" || len(component.Children) != 0 {
				return fmt.Errorf("Text %q must contain only text", component.ID)
			}
			if binding, ok := component.Text.(Binding); ok && !strings.HasPrefix(binding.Path, "/") {
				return fmt.Errorf("Text %q binding must be an absolute JSON Pointer", component.ID)
			}
		default:
			return fmt.Errorf("component %q uses unsupported display component %q", component.ID, component.Component)
		}
	}
	if _, exists := byID["root"]; !exists {
		return errors.New("components must contain exactly one root id")
	}
	for _, component := range components {
		references := component.Children
		if component.Child != "" {
			references = []string{component.Child}
		}
		for _, reference := range references {
			if _, exists := byID[reference]; !exists {
				return fmt.Errorf("component %q references unknown child %q", component.ID, reference)
			}
		}
	}
	return nil
}
