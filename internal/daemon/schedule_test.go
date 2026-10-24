package daemon

import (
	"testing"
	"time"
)

func TestScheduleDebounce(t *testing.T) {
	zero := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := newSchedule(time.Minute)
	s.begin(zero)
	s.complete(zero, zero, true)
	s.hint(zero, zero)
	if got := s.next(zero); !got.Equal(zero.Add(time.Second)) {
		t.Fatal(got)
	}
	s.hint(zero.Add(800*time.Millisecond), zero.Add(800*time.Millisecond))
	if got := s.next(zero); !got.Equal(zero.Add(1800 * time.Millisecond)) {
		t.Fatal(got)
	}
	for i := 1; i < 10; i++ {
		s.hint(zero.Add(time.Duration(i)*time.Second), zero.Add(time.Duration(i)*time.Second))
	}
	if got := s.next(zero); !got.Equal(zero.Add(5 * time.Second)) {
		t.Fatal(got)
	}
}
func TestSchedulePeriodicFairness(t *testing.T) {
	z := time.Now()
	s := newSchedule(time.Minute)
	s.begin(z)
	s.complete(z, z, true)
	s.hint(z.Add(10*time.Second), z.Add(10*time.Second))
	s.begin(z.Add(11 * time.Second))
	s.complete(z.Add(11*time.Second), z.Add(12*time.Second), true)
	if got := s.next(z); !got.Equal(z.Add(time.Minute)) {
		t.Fatal(got)
	}
	s.hint(z.Add(58*time.Second), z.Add(58*time.Second))
	s.begin(z.Add(59 * time.Second))
	s.complete(z.Add(59*time.Second), z.Add(70*time.Second), true)
	if got := s.next(z); !got.Equal(z.Add(130 * time.Second)) {
		t.Fatal(got)
	}
}
func TestScheduleRetryRestriction(t *testing.T) {
	z := time.Now()
	s := newSchedule(time.Minute)
	s.begin(z)
	s.complete(z, z.Add(10*time.Second), false)
	s.hint(z.Add(11*time.Second), z.Add(11*time.Second))
	if got := s.next(z); !got.Equal(z.Add(70 * time.Second)) {
		t.Fatal(got)
	}
	s.begin(z.Add(70 * time.Second))
	s.complete(z.Add(70*time.Second), z.Add(71*time.Second), true)
	s.hint(z.Add(72*time.Second), z.Add(72*time.Second))
	if got := s.next(z); !got.Equal(z.Add(73 * time.Second)) {
		t.Fatal(got)
	}
}
func TestScheduleHintsDuringAttempt(t *testing.T)     { testScheduleBoundary(t) }
func TestScheduleCompletionBoundaryHint(t *testing.T) { testScheduleBoundary(t) }
func testScheduleBoundary(t *testing.T) {
	t.Helper()
	z := time.Now()
	s := newSchedule(time.Minute)
	s.begin(z)
	s.hint(z.Add(time.Second), z.Add(time.Second))
	s.complete(z, z.Add(2*time.Second), true)
	if got := s.next(z); !got.Equal(z.Add(2 * time.Second)) {
		t.Fatal(got)
	}
	s.begin(z.Add(2 * time.Second))
	s.hint(z.Add(2*time.Second), z.Add(2*time.Second))
	s.complete(z.Add(2*time.Second), z.Add(3*time.Second), true)
	if got := s.next(z); !got.Equal(z.Add(3 * time.Second)) {
		t.Fatal(got)
	}
}
