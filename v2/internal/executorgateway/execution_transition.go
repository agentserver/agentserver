package executorgateway

import (
	"errors"
	"math"
	"sync"
)

// ExecutionTransitionAllocator owns the event/outbox identities emitted by
// one gateway process. A fresh producer instance ID must be used after every
// process restart; its in-memory sequence is intentionally not recoverable in
// Phase 1.
type ExecutionTransitionAllocator struct {
	mu                 sync.Mutex
	producerInstanceID string
	producerSeq        int64
	idGenerator        IDGenerator
}

func NewExecutionTransitionAllocator(producerInstanceID string, idGenerator IDGenerator) (*ExecutionTransitionAllocator, error) {
	if err := validateRegistryIdentity("transition producer instance ID", producerInstanceID); err != nil {
		return nil, err
	}
	if idGenerator == nil {
		return nil, errors.New("transition ID generator is required")
	}
	return &ExecutionTransitionAllocator{
		producerInstanceID: producerInstanceID,
		idGenerator:        idGenerator,
	}, nil
}

func NewDefaultExecutionTransitionAllocator(producerInstanceID string) (*ExecutionTransitionAllocator, error) {
	return NewExecutionTransitionAllocator(producerInstanceID, newRandomUUID)
}

// Allocate returns a process-wide strictly increasing producer sequence and
// two fresh UUID identities. Allocation is serialized so concurrent shell
// calls cannot reuse or reorder the local producer cursor. Failed ID
// generation does not consume a sequence.
func (allocator *ExecutionTransitionAllocator) Allocate() (ExecutionTransitionRecord, error) {
	if allocator == nil {
		return ExecutionTransitionRecord{}, errors.New("transition allocator is required")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.producerSeq == math.MaxInt64 {
		return ExecutionTransitionRecord{}, errors.New("transition producer sequence exhausted")
	}
	eventID, err := allocator.idGenerator()
	if err != nil {
		return ExecutionTransitionRecord{}, err
	}
	if err := validateRegistryIdentity("transition event ID", eventID); err != nil {
		return ExecutionTransitionRecord{}, err
	}
	outboxID, err := allocator.idGenerator()
	if err != nil {
		return ExecutionTransitionRecord{}, err
	}
	if err := validateRegistryIdentity("transition outbox ID", outboxID); err != nil {
		return ExecutionTransitionRecord{}, err
	}
	if eventID == outboxID || eventID == allocator.producerInstanceID || outboxID == allocator.producerInstanceID {
		return ExecutionTransitionRecord{}, errors.New("transition identities must be distinct")
	}
	allocator.producerSeq++
	return ExecutionTransitionRecord{
		EventID:            eventID,
		ProducerInstanceID: allocator.producerInstanceID,
		ProducerSeq:        allocator.producerSeq,
		OutboxID:           outboxID,
	}, nil
}
