package orchestrator

import (
	"container/heap"
	"sync"
	"time"

	"github.com/composecockpit/server/internal/domain"
)

type PriorityQueue struct {
	mu    sync.Mutex
	items priorityHeap
	cond  *sync.Cond
	closed bool
}

type queueItem struct {
	request  domain.OperationRequest
	index    int
}

type priorityHeap []*queueItem

func (h priorityHeap) Len() int { return len(h) }

func (h priorityHeap) Less(i, j int) bool {
	if h[i].request.Priority != h[j].request.Priority {
		return h[i].request.Priority > h[j].request.Priority
	}
	return h[i].request.CreatedAt.Before(h[j].request.CreatedAt)
}

func (h priorityHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *priorityHeap) Push(x interface{}) {
	item := x.(*queueItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *priorityHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

func NewPriorityQueue() *PriorityQueue {
	q := &PriorityQueue{
		items: make(priorityHeap, 0),
	}
	q.cond = sync.NewCond(&q.mu)
	heap.Init(&q.items)
	return q
}

func (q *PriorityQueue) Enqueue(req domain.OperationRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}

	heap.Push(&q.items, &queueItem{request: req})
	q.cond.Signal()
}

func (q *PriorityQueue) Dequeue() (domain.OperationRequest, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for q.items.Len() == 0 && !q.closed {
		q.cond.Wait()
	}

	if q.closed && q.items.Len() == 0 {
		return domain.OperationRequest{}, false
	}

	item := heap.Pop(&q.items).(*queueItem)
	return item.request, true
}

func (q *PriorityQueue) TryDequeue() (domain.OperationRequest, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.items.Len() == 0 {
		return domain.OperationRequest{}, false
	}

	item := heap.Pop(&q.items).(*queueItem)
	return item.request, true
}

func (q *PriorityQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items.Len()
}

func (q *PriorityQueue) Remove(opID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.request.ID == opID {
			heap.Remove(&q.items, i)
			return true
		}
	}
	return false
}

func (q *PriorityQueue) RemoveAndReturn(opID string) (domain.OperationRequest, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.request.ID == opID {
			heap.Remove(&q.items, i)
			return item.request, true
		}
	}
	return domain.OperationRequest{}, false
}

func (q *PriorityQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}
