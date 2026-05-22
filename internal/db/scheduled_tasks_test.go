package db

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustCreateWorkspace(t *testing.T, d *DB) string {
	t.Helper()
	wsID := "ws_test_sched_" + t.Name()
	_, err := d.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		wsID, "test workspace "+t.Name())
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM workspaces WHERE id = $1`, wsID) }) // CASCADE drops scheduled_tasks
	return wsID
}

func intStr(i int) string { return fmt.Sprintf("%04d", i) }

func strPtr(s string) *string { return &s }

func TestScheduledTask_InsertAndGet(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d)

	st := &ScheduledTask{
		ID: "sch_a", WorkspaceID: wsID, SeriesID: "sch_a",
		CreatorKind: "mcp", Prompt: "say hello",
		Timezone: "UTC", ProcessAfter: time.Now().Add(-1 * time.Second),
		Status: "pending", TimeoutSeconds: 600,
	}
	if err := d.CreateScheduledTask(st); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetScheduledTaskBySeries(wsID, "sch_a")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Prompt != "say hello" {
		t.Fatalf("got %#v", got)
	}
}

func TestScheduledTask_LeaseSkipLocked_Concurrent(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d)
	for i := 0; i < 20; i++ {
		st := &ScheduledTask{
			ID: "sch_" + intStr(i), WorkspaceID: wsID, SeriesID: "sch_" + intStr(i),
			CreatorKind: "mcp", Prompt: "p", Timezone: "UTC",
			ProcessAfter: time.Now().Add(-time.Second), Status: "pending",
			TimeoutSeconds: 600,
		}
		if err := d.CreateScheduledTask(st); err != nil {
			t.Fatal(err)
		}
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed = map[string]int{}
	)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(owner int) {
			defer wg.Done()
			leased, err := d.LeaseDueScheduledTasks(10, 60, "owner-"+intStr(owner))
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, t2 := range leased {
				claimed[t2.ID]++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for id, n := range claimed {
		if n != 1 {
			t.Errorf("task %s leased %d times, want 1", id, n)
		}
	}
	if len(claimed) != 20 {
		t.Errorf("claimed %d / 20", len(claimed))
	}
}

func TestScheduledTask_CancelMatchesBySeriesID(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d)
	// Two rows in the same series — one completed (prior occurrence), one pending (next).
	for _, st := range []*ScheduledTask{
		{ID: "sch_old", WorkspaceID: wsID, SeriesID: "sch_old", CreatorKind: "mcp",
			Prompt: "p", Timezone: "UTC", ProcessAfter: time.Now().Add(-time.Hour),
			Status: "completed", TimeoutSeconds: 600},
		{ID: "sch_new", WorkspaceID: wsID, SeriesID: "sch_old", CreatorKind: "mcp",
			Prompt: "p", Timezone: "UTC", ProcessAfter: time.Now().Add(time.Hour),
			Status: "pending", TimeoutSeconds: 600, Recurrence: strPtr("*/5 * * * *")},
	} {
		if err := d.CreateScheduledTask(st); err != nil {
			t.Fatal(err)
		}
	}

	n, err := d.CancelScheduledSeries(wsID, "sch_old")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cancelled %d, want 1 (only the live row)", n)
	}
	got, _ := d.GetScheduledTaskBySeries(wsID, "sch_old")
	if got.Status != "cancelled" {
		t.Fatalf("status=%s", got.Status)
	}
}
