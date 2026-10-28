//go:build darwin

package daemon

import "time"

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
