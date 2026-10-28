package daemon

import (
	"context"
	"sync"
	"time"
)

type RemoteCheckObservation struct {
	CheckedAt time.Time
	Outcome   string
}

type WatchBatch struct {
	Remote      *RemoteCheckObservation
	First, Last time.Time
	Err         error
}
type ChangeSource interface {
	Ready() <-chan struct{}
	Take() WatchBatch
	Close() error
}
type Watcher interface {
	ChangeSource
	Reconcile(context.Context) error
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
	m.batch.Remote = nil
	return b
}

func (m *watchMailbox) observeRemote(now time.Time, outcome string, changed bool, fault error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batch.Remote = &RemoteCheckObservation{CheckedAt: now.UTC(), Outcome: outcome}
	if changed {
		if m.batch.First.IsZero() {
			m.batch.First = now
		}
		m.batch.Last = now
	}
	if fault != nil && m.batch.Err == nil {
		m.batch.Err = fault
	}
	m.notify()
}
