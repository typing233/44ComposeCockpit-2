package orchestrator_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/orchestrator"
)

func TestInMemoryLocker_AcquireRelease(t *testing.T) {
	locker := orchestrator.NewInMemoryLocker()
	ctx := context.Background()
	projectID := domain.ProjectID("test-project")

	release, err := locker.Acquire(ctx, projectID, "op-1")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	locked, holder := locker.IsLocked(projectID)
	if !locked || holder != "op-1" {
		t.Errorf("expected locked by op-1, got locked=%v holder=%s", locked, holder)
	}

	release()

	locked, _ = locker.IsLocked(projectID)
	if locked {
		t.Error("expected unlocked after release")
	}
}

func TestInMemoryLocker_ConcurrentAcquire(t *testing.T) {
	locker := orchestrator.NewInMemoryLocker()
	projectID := domain.ProjectID("test-project")

	release, err := locker.Acquire(context.Background(), projectID, "op-1")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rel, err := locker.Acquire(ctx, projectID, "op-2")
		if err != nil {
			t.Errorf("second acquire failed: %v", err)
			return
		}
		close(acquired)
		rel()
	}()

	time.Sleep(100 * time.Millisecond)
	release()

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("second acquire timed out")
	}
}

func TestInMemoryLocker_TimeoutOnLock(t *testing.T) {
	locker := orchestrator.NewInMemoryLocker()
	projectID := domain.ProjectID("test-project")

	release, _ := locker.Acquire(context.Background(), projectID, "op-1")
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := locker.Acquire(ctx, projectID, "op-2")
	if err == nil {
		t.Fatal("expected error when lock times out")
	}
}

func TestInMemoryLocker_DifferentProjects(t *testing.T) {
	locker := orchestrator.NewInMemoryLocker()
	ctx := context.Background()

	rel1, err := locker.Acquire(ctx, domain.ProjectID("p1"), "op-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rel1()

	rel2, err := locker.Acquire(ctx, domain.ProjectID("p2"), "op-2")
	if err != nil {
		t.Fatal("different projects should not contend")
	}
	defer rel2()
}

func TestPriorityQueue_Ordering(t *testing.T) {
	q := orchestrator.NewPriorityQueue()

	q.Enqueue(domain.OperationRequest{ID: "low", Priority: 10, CreatedAt: time.Now()})
	q.Enqueue(domain.OperationRequest{ID: "high", Priority: 100, CreatedAt: time.Now()})
	q.Enqueue(domain.OperationRequest{ID: "mid", Priority: 50, CreatedAt: time.Now()})

	first, ok := q.TryDequeue()
	if !ok || first.ID != "high" {
		t.Errorf("expected high priority first, got %s", first.ID)
	}

	second, ok := q.TryDequeue()
	if !ok || second.ID != "mid" {
		t.Errorf("expected mid priority second, got %s", second.ID)
	}

	third, ok := q.TryDequeue()
	if !ok || third.ID != "low" {
		t.Errorf("expected low priority third, got %s", third.ID)
	}
}

func TestPriorityQueue_SamePriority_FIFO(t *testing.T) {
	q := orchestrator.NewPriorityQueue()

	q.Enqueue(domain.OperationRequest{ID: "first", Priority: 50, CreatedAt: time.Now()})
	time.Sleep(time.Millisecond)
	q.Enqueue(domain.OperationRequest{ID: "second", Priority: 50, CreatedAt: time.Now()})

	item, _ := q.TryDequeue()
	if item.ID != "first" {
		t.Errorf("expected FIFO for same priority, got %s first", item.ID)
	}
}

func TestPriorityQueue_Remove(t *testing.T) {
	q := orchestrator.NewPriorityQueue()

	q.Enqueue(domain.OperationRequest{ID: "op-1", Priority: 50})
	q.Enqueue(domain.OperationRequest{ID: "op-2", Priority: 50})

	removed := q.Remove("op-1")
	if !removed {
		t.Error("expected successful removal")
	}

	if q.Len() != 1 {
		t.Errorf("expected 1 item remaining, got %d", q.Len())
	}

	item, _ := q.TryDequeue()
	if item.ID != "op-2" {
		t.Errorf("expected op-2 remaining, got %s", item.ID)
	}
}

func TestPriorityQueue_ConcurrentEnqueueDequeue(t *testing.T) {
	q := orchestrator.NewPriorityQueue()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			q.Enqueue(domain.OperationRequest{
				ID:       string(rune('a' + id%26)),
				Priority: id % 10,
			})
		}(i)
	}
	wg.Wait()

	if q.Len() != 100 {
		t.Errorf("expected 100 items, got %d", q.Len())
	}
}
