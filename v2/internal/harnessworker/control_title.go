package harnessworker

import (
	"context"
	"errors"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/sessiontitle"
)

func (client *WorkerControlClient) SendSessionTitle(ctx context.Context, proposal sessiontitle.Proposal) error {
	if err := proposal.Validate(); err != nil {
		return err
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID == "" || client.turnID == "" || client.terminalSent {
		return errors.New("title proposal is outside an accepted turn")
	}
	return client.sendControlEvent(ctx, harnesscontrol.SessionTitleEvent{Kind: harnesscontrol.EventKindSessionTitle, Title: proposal.Title, Source: proposal.Source}, false, false)
}
