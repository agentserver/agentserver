package harnesspool

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func TestControlIdentityAllocatorIsConcurrentAndMonotonic(t *testing.T) {
	next := 100
	allocator, err := NewControlIdentityAllocator("70000000-0000-4000-8000-000000000001", func() (string, error) {
		next++
		return fmt.Sprintf("70000000-0000-4000-8000-%012x", next), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	results := make(chan RunAttemptClaimIdentity, count)
	errorsChannel := make(chan error, count)
	var waitGroup sync.WaitGroup
	for range count {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			identity, err := allocator.AllocateRunAttemptClaim()
			results <- identity
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	sequences := make([]int, 0, count)
	identities := make(map[string]struct{}, count*3)
	for result := range results {
		sequences = append(sequences, int(result.Record.ProducerSeq))
		for _, identity := range []string{result.RunAttemptID, result.Record.EventID, result.Record.OutboxID} {
			if _, exists := identities[identity]; exists {
				t.Fatalf("identity %s was reused", identity)
			}
			identities[identity] = struct{}{}
		}
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("producer sequences = %v", sequences)
		}
	}
}

func TestControlIdentityAllocatorDoesNotConsumeSequenceOnGenerationFailure(t *testing.T) {
	calls := 0
	allocator, err := NewControlIdentityAllocator("70000000-0000-4000-8000-000000000001", func() (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("entropy unavailable")
		}
		return fmt.Sprintf("70000000-0000-4000-8000-%012x", 100+calls), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.AllocateRunAttemptClaim(); err == nil {
		t.Fatal("identity generation failure was ignored")
	}
	identity, err := allocator.AllocateRunAttemptClaim()
	if err != nil {
		t.Fatal(err)
	}
	if identity.Record.ProducerSeq != 1 {
		t.Fatalf("producer sequence after failed allocation = %d, want 1", identity.Record.ProducerSeq)
	}
}

func TestControlIdentityAllocatorSharesCursorWithStandaloneTransitions(t *testing.T) {
	next := 500
	allocator, err := NewControlIdentityAllocator("70000000-0000-4000-8000-000000000001", func() (string, error) {
		next++
		return fmt.Sprintf("70000000-0000-4000-8000-%012x", next), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := allocator.AllocateTransitionRecord()
	if err != nil {
		t.Fatal(err)
	}
	claim, err := allocator.AllocateRunAttemptClaim()
	if err != nil {
		t.Fatal(err)
	}
	last, err := allocator.AllocateTransitionRecord()
	if err != nil {
		t.Fatal(err)
	}
	if first.ProducerSeq != 1 || claim.Record.ProducerSeq != 2 || last.ProducerSeq != 3 {
		t.Fatalf("shared producer cursor = %d, %d, %d", first.ProducerSeq, claim.Record.ProducerSeq, last.ProducerSeq)
	}
	seen := map[string]bool{}
	for _, identity := range []string{
		first.EventID, first.OutboxID, claim.RunAttemptID, claim.Record.EventID,
		claim.Record.OutboxID, last.EventID, last.OutboxID,
	} {
		if seen[identity] {
			t.Fatalf("control identity %s was reused", identity)
		}
		seen[identity] = true
	}
}
