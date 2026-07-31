package harnesspool

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"sync"
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type IDGenerator func() (string, error)

type RunAttemptClaimIdentity struct {
	RunAttemptID string
	Record       TransitionRecord
}

// ControlIdentityAllocator owns the monotonic producer cursor and immutable
// transition IDs emitted by one harness-pool process. A restart must use a
// fresh producer instance ID.
type ControlIdentityAllocator struct {
	mu                 sync.Mutex
	producerInstanceID string
	producerSeq        int64
	idGenerator        IDGenerator
}

func NewControlIdentityAllocator(producerInstanceID string, idGenerator IDGenerator) (*ControlIdentityAllocator, error) {
	if err := validateUUIDIdentity("producer instance ID", producerInstanceID); err != nil {
		return nil, err
	}
	if idGenerator == nil {
		return nil, errors.New("control identity generator is required")
	}
	return &ControlIdentityAllocator{producerInstanceID: producerInstanceID, idGenerator: idGenerator}, nil
}

func NewDefaultControlIdentityAllocator(producerInstanceID string) (*ControlIdentityAllocator, error) {
	return NewControlIdentityAllocator(producerInstanceID, newRandomUUID)
}

func (allocator *ControlIdentityAllocator) AllocateRunAttemptClaim() (RunAttemptClaimIdentity, error) {
	if allocator == nil {
		return RunAttemptClaimIdentity{}, errors.New("control identity allocator is required")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.producerSeq == math.MaxInt64 {
		return RunAttemptClaimIdentity{}, errors.New("control producer sequence exhausted")
	}
	runAttemptID, err := allocator.generateDistinct("run attempt ID", allocator.producerInstanceID)
	if err != nil {
		return RunAttemptClaimIdentity{}, err
	}
	eventID, err := allocator.generateDistinct("transition event ID", allocator.producerInstanceID, runAttemptID)
	if err != nil {
		return RunAttemptClaimIdentity{}, err
	}
	outboxID, err := allocator.generateDistinct("transition outbox ID", allocator.producerInstanceID, runAttemptID, eventID)
	if err != nil {
		return RunAttemptClaimIdentity{}, err
	}
	allocator.producerSeq++
	return RunAttemptClaimIdentity{
		RunAttemptID: runAttemptID,
		Record: TransitionRecord{
			EventID: eventID, ProducerInstanceID: allocator.producerInstanceID,
			ProducerSeq: allocator.producerSeq, OutboxID: outboxID,
		},
	}, nil
}

func (allocator *ControlIdentityAllocator) generateDistinct(field string, excluded ...string) (string, error) {
	value, err := allocator.idGenerator()
	if err != nil {
		return "", err
	}
	if err := validateUUIDIdentity(field, value); err != nil {
		return "", err
	}
	for _, existing := range excluded {
		if value == existing {
			return "", errors.New("control identities must be distinct")
		}
	}
	return value, nil
}

func validateUUIDIdentity(field, value string) error {
	if !canonicalUUIDPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
		return errors.New(field + " must be a non-zero lowercase canonical UUID")
	}
	return nil
}

func newRandomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:]), nil
}
