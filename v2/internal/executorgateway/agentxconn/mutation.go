package agentxconn

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type MutationDisposition string

const (
	MutationExecute   MutationDisposition = "execute"
	MutationPending   MutationDisposition = "pending"
	MutationCompleted MutationDisposition = "completed"
	MutationAmbiguous MutationDisposition = "ambiguous"
)

type MutationBeginResult struct {
	Disposition MutationDisposition
	Response    json.RawMessage
}

type mutationEntry struct {
	requestHash [sha256.Size]byte
	disposition MutationDisposition
	response    json.RawMessage
	cause       string
}

// MutationJournal is the agentx-side, exec-session-scoped dedupe kernel. It
// never evicts an accepted key while the session is usable: admission fails
// before execution when the bound is full. Losing this entire object requires
// rejecting resume; it must not be replaced and treated as an empty journal
// for an old session.
type MutationJournal struct {
	mu sync.Mutex

	limits     Limits
	maxEntries int
	entries    map[string]*mutationEntry
	closed     bool
}

func NewMutationJournal(maxEntries int, limits Limits) (*MutationJournal, error) {
	if maxEntries < 1 {
		return nil, errors.New("mutation journal max entries must be positive")
	}
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	return &MutationJournal{
		limits:     limits,
		maxEntries: maxEntries,
		entries:    make(map[string]*mutationEntry),
	}, nil
}

// Begin returns MutationExecute exactly once for a key. Callers must retain
// the entry before forwarding the deterministic RPC to a stock child.
func (journal *MutationJournal) Begin(mutationKey string, requestHash [sha256.Size]byte) (MutationBeginResult, error) {
	if err := validateUUID("mutationKey", mutationKey); err != nil {
		return MutationBeginResult{}, err
	}
	if requestHash == ([sha256.Size]byte{}) {
		return MutationBeginResult{}, errors.New("mutation request hash must not be zero")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return MutationBeginResult{}, protocolError(ErrorAmbiguous, true, "mutation journal is closed")
	}
	if entry := journal.entries[mutationKey]; entry != nil {
		if entry.requestHash != requestHash {
			return MutationBeginResult{}, protocolError(ErrorMutationConflict, true, "mutation key %s was reused with different request bytes", mutationKey)
		}
		return MutationBeginResult{
			Disposition: entry.disposition,
			Response:    append(json.RawMessage(nil), entry.response...),
		}, nil
	}
	if len(journal.entries) >= journal.maxEntries {
		return MutationBeginResult{}, protocolError(ErrorJournalFull, false, "mutation journal is full")
	}
	journal.entries[mutationKey] = &mutationEntry{
		requestHash: requestHash,
		disposition: MutationPending,
	}
	return MutationBeginResult{Disposition: MutationExecute}, nil
}

func (journal *MutationJournal) Complete(mutationKey string, requestHash [sha256.Size]byte, response json.RawMessage) error {
	if err := validateMutationResponse(response, journal.limits); err != nil {
		return err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return protocolError(ErrorAmbiguous, true, "mutation journal is closed")
	}
	entry := journal.entries[mutationKey]
	if entry == nil || entry.requestHash != requestHash {
		return protocolError(ErrorMutationConflict, true, "completion does not match a pending mutation")
	}
	if entry.disposition == MutationAmbiguous {
		return protocolError(ErrorAmbiguous, true, "mutation is already ambiguous: %s", entry.cause)
	}
	if entry.disposition == MutationCompleted {
		if !bytes.Equal(entry.response, response) {
			return protocolError(ErrorMutationConflict, true, "mutation completion changed its response")
		}
		return nil
	}
	entry.disposition = MutationCompleted
	entry.response = append(json.RawMessage(nil), response...)
	return nil
}

func (journal *MutationJournal) MarkAmbiguous(mutationKey string, requestHash [sha256.Size]byte, cause string) error {
	if err := validateText("ambiguous cause", cause, maxProtocolTextBytes); err != nil {
		return err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return protocolError(ErrorAmbiguous, true, "mutation journal is closed")
	}
	entry := journal.entries[mutationKey]
	if entry == nil || entry.requestHash != requestHash {
		return protocolError(ErrorMutationConflict, true, "ambiguous mutation does not match a pending key")
	}
	if entry.disposition == MutationCompleted {
		return protocolError(ErrorMutationConflict, true, "completed mutation cannot become ambiguous")
	}
	if entry.disposition == MutationAmbiguous {
		if entry.cause != cause {
			return protocolError(ErrorMutationConflict, true, "ambiguous mutation changed its cause")
		}
		return nil
	}
	entry.disposition = MutationAmbiguous
	entry.cause = cause
	return nil
}

func (journal *MutationJournal) Close() {
	journal.mu.Lock()
	journal.closed = true
	journal.mu.Unlock()
}

func validateMutationResponse(response json.RawMessage, limits Limits) error {
	if len(response) == 0 {
		return errors.New("mutation response is empty")
	}
	if len(response) > limits.MaxFrameBytes {
		return fmt.Errorf("mutation response is %d bytes, limit is %d", len(response), limits.MaxFrameBytes)
	}
	if err := validateJSONDocument(response, limits.MaxJSONValues, limits.MaxJSONDepth); err != nil {
		return fmt.Errorf("validate mutation response JSON: %w", err)
	}
	return nil
}
