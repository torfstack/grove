package daemon

import (
	"context"
	"sync"
	"time"
)

type WatchBatch struct {
	First, Last time.Time
	Err         error
}
type Watcher interface {
	Ready() <-chan struct{}
	Take() WatchBatch
	Reconcile(context.Context) error
	Close() error
}
type watchMailbox struct {
	mu    sync.Mutex
	ready chan struct{}
	batch WatchBatch
}

func newWatchMailbox() *watchMailbox { return &watchMailbox{ready: make(chan struct{}, 1)} }
func (m *watchMailbox) notify() {
	select {
	case m.ready <- struct{}{}:
	default:
	}
}
func (m *watchMailbox) hint(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batch.First.IsZero() {
		m.batch.First = now
	}
	m.batch.Last = now
	m.notify()
}
func (m *watchMailbox) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batch.Err == nil {
		m.batch.Err = err
	}
	m.notify()
}
func (m *watchMailbox) take() WatchBatch {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.ready:
	default:
	}
	b := m.batch
	m.batch.First = time.Time{}
	m.batch.Last = time.Time{}
	return b
}
