package workspacerepository

import "errors"

// Binding is a session's immutable repository choice. Workspace defaults may
// change independently; this source and checkout identity never move a dirty
// session to a different repository or region. WorkingDirectory in Source is
// the initial default, not a later per-run directory override.
type Binding struct {
	CheckoutID            string `json:"checkoutId"`
	Source                Source `json:"source"`
	SourceVersion         int64  `json:"sourceVersion"`
	Region                string `json:"region"`
	EnvironmentID         string `json:"environmentId"`
	ManagedSettingVersion int64  `json:"managedSettingVersion"`
}

func (b Binding) Validate() error {
	for _, id := range []string{b.CheckoutID, b.EnvironmentID} {
		if !bindingPattern.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return errors.New("repository binding identity is invalid")
		}
	}
	if b.SourceVersion < 1 || b.SourceVersion > 1<<53-1 || b.ManagedSettingVersion < 1 || b.ManagedSettingVersion > 1<<53-1 {
		return errors.New("repository setting version is invalid")
	}
	if b.Region != "cn" && b.Region != "sg" {
		return errors.New("repository sessions require a CN or SG Kubernetes profile")
	}
	return b.Source.Validate()
}
