package agentxconn

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMutationJournalAllowsExactlyOneExternalExecution(t *testing.T) {
	journal, err := NewMutationJournal(8, testWireLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := "61000000-0000-0000-0000-000000000006"
	hash := sha256.Sum256([]byte("process/start params"))
	var executes atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, beginErr := journal.Begin(key, hash)
			if beginErr != nil {
				t.Errorf("Begin() error = %v", beginErr)
				return
			}
			if result.Disposition == MutationExecute {
				executes.Add(1)
			} else if result.Disposition != MutationPending {
				t.Errorf("concurrent disposition = %s", result.Disposition)
			}
		}()
	}
	wait.Wait()
	if got := executes.Load(); got != 1 {
		t.Fatalf("external execution permissions = %d, want 1", got)
	}

	response := json.RawMessage(`{"id":"start-1","result":{"processId":"70000000-0000-0000-0000-000000000007"}}`)
	if err := journal.Complete(key, hash, response); err != nil {
		t.Fatal(err)
	}
	replay, err := journal.Begin(key, hash)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != MutationCompleted || string(replay.Response) != string(response) {
		t.Fatalf("completed replay = %+v", replay)
	}
	replay.Response[0] = 'x'
	again, _ := journal.Begin(key, hash)
	if again.Response[0] != '{' {
		t.Fatal("mutation journal exposed mutable response bytes")
	}
}

func TestMutationJournalFailsClosedOnConflictAmbiguityAndCapacity(t *testing.T) {
	journal, err := NewMutationJournal(1, testWireLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := "61000000-0000-0000-0000-000000000006"
	hash := sha256.Sum256([]byte("request one"))
	if result, err := journal.Begin(key, hash); err != nil || result.Disposition != MutationExecute {
		t.Fatalf("first Begin() = %+v, %v", result, err)
	}
	changed := sha256.Sum256([]byte("request two"))
	if _, err := journal.Begin(key, changed); codeOf(err) != ErrorMutationConflict {
		t.Fatalf("mutation conflict error = %v", err)
	}
	if _, err := journal.Begin("62000000-0000-0000-0000-000000000006", changed); codeOf(err) != ErrorJournalFull {
		t.Fatalf("mutation capacity error = %v", err)
	}
	if err := journal.MarkAmbiguous(key, hash, "child exited before terminal evidence"); err != nil {
		t.Fatal(err)
	}
	if err := journal.MarkAmbiguous(key, hash, "different cause"); codeOf(err) != ErrorMutationConflict {
		t.Fatalf("changed ambiguous cause error = %v", err)
	}
	result, err := journal.Begin(key, hash)
	if err != nil || result.Disposition != MutationAmbiguous {
		t.Fatalf("ambiguous replay = %+v, %v", result, err)
	}
	if err := journal.Complete(key, hash, json.RawMessage(`{"result":{}}`)); codeOf(err) != ErrorAmbiguous {
		t.Fatalf("ambiguous completion error = %v", err)
	}
	journal.Close()
	if _, err := journal.Begin(key, hash); codeOf(err) != ErrorAmbiguous {
		t.Fatalf("closed journal error = %v", err)
	}
	if err := journal.Complete(key, hash, json.RawMessage(`{"result":{}}`)); codeOf(err) != ErrorAmbiguous {
		t.Fatalf("closed journal completion error = %v", err)
	}
	if err := journal.MarkAmbiguous(key, hash, "child exited before terminal evidence"); codeOf(err) != ErrorAmbiguous {
		t.Fatalf("closed journal ambiguity error = %v", err)
	}
}
