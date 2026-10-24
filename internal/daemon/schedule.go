package daemon

import "time"

type schedule struct {
	interval                     time.Duration
	periodic, retry, first, last time.Time
}

func newSchedule(interval time.Duration) *schedule { return &schedule{interval: interval} }
func (s *schedule) hint(first, last time.Time) {
	if first.IsZero() {
		return
	}
	if s.first.IsZero() || first.Before(s.first) {
		s.first = first
	}
	if last.After(s.last) {
		s.last = last
	}
}
func (s *schedule) begin(_ time.Time) { s.first = time.Time{}; s.last = time.Time{} }
func (s *schedule) complete(_, finished time.Time, succeeded bool) {
	if s.periodic.IsZero() || !finished.Before(s.periodic) {
		s.periodic = finished.Add(s.interval)
	}
	if succeeded {
		s.retry = time.Time{}
	} else {
		s.retry = finished.Add(s.interval)
	}
}
func (s *schedule) next(now time.Time) time.Time {
	if s.periodic.IsZero() {
		return now
	}
	due := s.periodic
	if !s.first.IsZero() {
		event := s.last.Add(time.Second)
		if limit := s.first.Add(5 * time.Second); limit.Before(event) {
			event = limit
		}
		if s.retry.After(event) {
			event = s.retry
		}
		if event.Before(due) {
			due = event
		}
	}
	return due
}
