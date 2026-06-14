package orchestrator

import (
	"context"
	"fmt"
	"sync"

	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/pkg/apierr"
)

type ProjectLocker interface {
	Acquire(ctx context.Context, projectID domain.ProjectID, opID string) (release func(), err error)
	TryAcquire(ctx context.Context, projectID domain.ProjectID, opID string) (release func(), acquired bool, err error)
	IsLocked(projectID domain.ProjectID) (bool, string)
}

type inMemoryLocker struct {
	mu    sync.Mutex
	locks map[domain.ProjectID]*lockEntry
}

type lockEntry struct {
	holder string
	ch     chan struct{}
}

func NewInMemoryLocker() ProjectLocker {
	return &inMemoryLocker{
		locks: make(map[domain.ProjectID]*lockEntry),
	}
}

func (l *inMemoryLocker) Acquire(ctx context.Context, projectID domain.ProjectID, opID string) (func(), error) {
	for {
		l.mu.Lock()
		entry, locked := l.locks[projectID]
		if !locked {
			l.locks[projectID] = &lockEntry{holder: opID, ch: make(chan struct{})}
			l.mu.Unlock()
			return l.releaseFunc(projectID), nil
		}
		waitCh := entry.ch
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, &domain.AppError{
				Code:       apierr.ErrProjectLocked,
				Message:    fmt.Sprintf("project %s is locked by operation %s", projectID, entry.holder),
				HTTPStatus: 409,
			}
		case <-waitCh:
		}
	}
}

func (l *inMemoryLocker) TryAcquire(ctx context.Context, projectID domain.ProjectID, opID string) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, locked := l.locks[projectID]; locked {
		return nil, false, nil
	}

	l.locks[projectID] = &lockEntry{holder: opID, ch: make(chan struct{})}
	return l.releaseFunc(projectID), true, nil
}

func (l *inMemoryLocker) IsLocked(projectID domain.ProjectID) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := l.locks[projectID]; ok {
		return true, entry.holder
	}
	return false, ""
}

func (l *inMemoryLocker) releaseFunc(projectID domain.ProjectID) func() {
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if entry, ok := l.locks[projectID]; ok {
			close(entry.ch)
			delete(l.locks, projectID)
		}
	}
}
