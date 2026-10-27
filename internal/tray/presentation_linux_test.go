package tray

import (
	"errors"
	"github.com/torfstack/grove/internal/daemon"
	"strings"
	"testing"
)

func healthy() Sample {
	return Sample{Status: daemon.StatusReport{Owner: "active", ProfileState: "available", Profile: &daemon.ProfileView{PopulationComplete: true}, LastRecorded: &daemon.Observation{Phase: "waiting", Outcome: "completed", Mode: daemon.ModeDownload}}}
}

func TestTrayPresentation(t *testing.T) {
	cases := []struct {
		name               string
		alter              func(*Sample)
		icon, status, text string
	}{
		{"waiting", func(*Sample) {}, "grove-idle", "Active", "Last sync completed"},
		{"stopped", func(s *Sample) { s.Status.Owner = "inactive" }, "grove-stopped", "Active", "Daemon stopped"},
		{"syncing busy", func(s *Sample) {
			s.Status.LastRecorded.Phase = "syncing"
			s.Status.Profile = nil
			s.Status.ProfileState = "busy"
		}, "grove-syncing", "Active", "Syncing (last recorded)"},
		{"failed", func(s *Sample) { s.Status.LastRecorded.Outcome = "failed" }, "grove-attention", "NeedsAttention", "Last sync failed"},
		{"busy", func(s *Sample) { s.Status.LastRecorded.Outcome = "busy" }, "grove-idle", "Active", "Last sync busy"},
		{"cancelled", func(s *Sample) { s.Status.LastRecorded.Outcome = "cancelled" }, "grove-idle", "Active", "Last sync cancelled"},
		{"conflicts", func(s *Sample) { s.Status.Profile.ConflictCount = 2 }, "grove-attention", "NeedsAttention", "Conflicts: 2"},
		{"recovery", func(s *Sample) { s.Status.Profile.RecoveryPending = true }, "grove-attention", "NeedsAttention", "Recovery pending"},
		{"unknown", func(s *Sample) { s.Status.Owner = "unknown" }, "grove-attention", "NeedsAttention", "Status unavailable"},
		{"error", func(s *Sample) { s.Err = errors.New("secret-path") }, "grove-attention", "NeedsAttention", "Status unavailable"},
		{"remote failure", func(s *Sample) { s.Status.LastRecorded.RemoteOutcome = "failed" }, "grove-attention", "NeedsAttention", "Last remote check failed"},
		{"no record", func(s *Sample) { s.Status.LastRecorded = nil }, "grove-idle", "Active", "No completed attempt recorded"},
		{"stale stopped", func(s *Sample) { s.Status.LastRecorded.Phase = "stopped" }, "grove-idle", "Active", "Last recorded phase: stopped"},
		{"incomplete", func(s *Sample) { s.Status.Profile.PopulationComplete = false }, "grove-idle", "Active", "Population incomplete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{Name: "Personal", ProfileDir: "/private/profile"}
			memory, _ := Reduce(Memory{}, healthy(), ServiceState{}, opts)
			s := healthy()
			tc.alter(&s)
			_, v := Reduce(memory, s, ServiceState{}, opts)
			if v.IconName != tc.icon || v.Status != tc.status || !strings.Contains(v.TooltipText, tc.text) {
				t.Fatalf("view: %+v", v)
			}
			text := v.TooltipTitle + v.TooltipText
			if strings.Contains(text, "Up to date") || strings.Contains(text, "secret-path") || strings.Contains(text, "/private") {
				t.Fatalf("misleading/private: %s", text)
			}
		})
	}
}

func TestTrayRetainedFacts(t *testing.T) {
	opts := Options{Name: "Grove", ProfileDir: "/one"}
	s := healthy()
	s.Status.Profile.ConflictCount = 3
	s.Status.Profile.RecoveryPending = true
	memory, _ := Reduce(Memory{}, s, ServiceState{}, opts)
	busy := healthy()
	busy.Status.Profile = nil
	busy.Status.ProfileState = "busy"
	memory, v := Reduce(memory, busy, ServiceState{}, opts)
	if v.Status != "NeedsAttention" || !strings.Contains(v.TooltipText, "Last known conflicts: 3") || !strings.Contains(v.TooltipText, "Profile details busy") {
		t.Fatalf("lost known facts: %+v", v)
	}
	failed := healthy()
	failed.Err = errors.New("private")
	failed.Status.Profile.ConflictCount = 0
	memory, v = Reduce(memory, failed, ServiceState{}, opts)
	if !strings.Contains(v.TooltipText, "Last known conflicts: 3") {
		t.Fatal("failed inspection cleared facts")
	}
	_, v = Reduce(memory, healthy(), ServiceState{}, opts)
	if v.Status != "Active" || strings.Contains(v.TooltipText, "Last known") {
		t.Fatalf("fresh facts not restored: %+v", v)
	}
	opts.ProfileDir = "/other"
	_, v = Reduce(memory, busy, ServiceState{}, opts)
	if strings.Contains(v.TooltipText, "conflicts: 3") {
		t.Fatal("facts crossed profile identity")
	}
	_, v = Reduce(Memory{}, healthy(), ServiceState{Unit: "groved@personal.service", Phase: "inactive", CanStart: true}, Options{Name: "Personal"})
	for _, row := range v.Rows {
		if row.Action == ActionStart && row.Enabled {
			t.Fatal("start enabled over active owner")
		}
	}
}

func TestTrayStartupBusyHistory(t *testing.T) {
	for _, phase := range []string{"waiting", "syncing", "stopped"} {
		for _, owner := range []string{"active", "inactive"} {
			t.Run(phase+" "+owner, func(t *testing.T) {
				sample := healthy()
				sample.Status.Owner = owner
				sample.Status.LastRecorded.Phase = phase
				sample.Status.Profile = nil
				sample.Status.ProfileState = "busy"
				_, view := Reduce(Memory{}, sample, ServiceState{}, Options{Name: "Grove", ProfileDir: "/profile"})
				if view.Status != "NeedsAttention" || view.IconName != "grove-attention" || !strings.Contains(view.TooltipText, "Status unavailable") {
					t.Fatalf("unvalidated profile looks normal: %+v", view)
				}
				if !strings.Contains(view.TooltipText, "Last recorded phase: "+phase) {
					t.Fatal("historical phase discarded")
				}
			})
		}
	}
}
