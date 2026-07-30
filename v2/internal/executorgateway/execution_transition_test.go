package executorgateway

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func TestExecutionTransitionAllocatorIsConcurrentAndMonotonic(t *testing.T) {
	const allocations = 128
	var generatorMu sync.Mutex
	generated := 0
	generator := func() (string, error) {
		generatorMu.Lock()
		defer generatorMu.Unlock()
		generated++
		return fmt.Sprintf("70000000-0000-4000-8000-%012d", generated), nil
	}
	allocator, err := NewExecutionTransitionAllocator("71000000-0000-4000-8000-000000000001", generator)
	if err != nil {
		t.Fatal(err)
	}
	records := make(chan ExecutionTransitionRecord, allocations)
	var group sync.WaitGroup
	for range allocations {
		group.Add(1)
		go func() {
			defer group.Done()
			record, allocateErr := allocator.Allocate()
			if allocateErr != nil {
				t.Errorf("Allocate() error = %v", allocateErr)
				return
			}
			records <- record
		}()
	}
	group.Wait()
	close(records)
	sequences := make([]int, 0, allocations)
	identities := make(map[string]struct{}, allocations*2)
	for record := range records {
		if record.ProducerInstanceID != "71000000-0000-4000-8000-000000000001" {
			t.Errorf("producer instance = %q", record.ProducerInstanceID)
		}
		sequences = append(sequences, int(record.ProducerSeq))
		for _, identity := range []string{record.EventID, record.OutboxID} {
			if _, duplicate := identities[identity]; duplicate {
				t.Errorf("duplicate transition identity %q", identity)
			}
			identities[identity] = struct{}{}
		}
	}
	sort.Ints(sequences)
	if len(sequences) != allocations {
		t.Fatalf("allocated %d records, want %d", len(sequences), allocations)
	}
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("producer sequences = %v", sequences)
		}
	}
}

func TestExecutionTransitionAllocatorDoesNotConsumeSequenceOnFailure(t *testing.T) {
	calls := 0
	allocator, err := NewExecutionTransitionAllocator("71000000-0000-4000-8000-000000000001", func() (string, error) {
		calls++
		if calls == 1 {
			return "", fmt.Errorf("entropy unavailable")
		}
		return fmt.Sprintf("72000000-0000-4000-8000-%012d", calls), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate(); err == nil {
		t.Fatal("failed ID generation was accepted")
	}
	record, err := allocator.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if record.ProducerSeq != 1 {
		t.Fatalf("producer sequence after retry = %d, want 1", record.ProducerSeq)
	}
}
