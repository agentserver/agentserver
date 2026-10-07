package harnesscontrol

import "github.com/agentserver/agentserver/v2/internal/sessiontitle"

func (event SessionTitleEvent) Validate() error {
	if event.Kind != EventKindSessionTitle {
		return malformed("invalid session title event kind")
	}
	if err := (sessiontitle.Proposal{Title: event.Title, Source: event.Source}).Validate(); err != nil {
		return malformed("invalid session title: %v", err)
	}
	return nil
}
