package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/torfstack/grove/internal/daemon"
)

func Reduce(previous Memory, sample Sample, service ServiceState, opts Options) (Memory, View) {
	m := previous
	if m.profile != opts.ProfileDir {
		m = Memory{profile: opts.ProfileDir}
	}
	report := sample.Status
	fresh := sample.Err == nil && report.ProfileState == "available" && report.Profile != nil
	if fresh {
		facts := *report.Profile
		m.facts = &facts
	}
	v := View{Status: "Active", IconName: "grove-idle", TooltipTitle: opts.Name}
	owner := "unknown"
	if report.Owner == "active" || report.Owner == "inactive" {
		owner = report.Owner
	}
	lines := []string{"Daemon ownership: " + owner}
	headline := "Running"
	if owner == "inactive" {
		headline = "Daemon stopped"
		v.IconName = "grove-stopped"
	}
	observed := report.LastRecorded
	if observed != nil {
		phase := safePhase(observed.Phase)
		lines = append(lines, "Last recorded phase: "+phase)
		outcome := attemptLabel(observed.Outcome)
		lines = append(lines, outcome)
		if observed.AttemptFinishedAt != nil {
			lines = append(lines, "Last attempt finished: "+observed.AttemptFinishedAt.UTC().Format(time.RFC3339))
		}
		mode := "unknown"
		if observed.Mode == daemon.ModeDownload || observed.Mode == daemon.ModeTwoWay {
			mode = string(observed.Mode)
		}
		lines = append(lines, "Mode: "+mode)
		if owner == "active" && phase == "syncing" {
			headline = "Syncing (last recorded)"
			v.IconName = "grove-syncing"
		}
		if owner == "active" && phase != "syncing" && observed.Outcome == "failed" {
			headline = "Last sync failed"
			v.IconName = "grove-attention"
			v.Status = "NeedsAttention"
		}
		if observed.RemoteOutcome == "failed" {
			lines = append(lines, "Last remote check failed")
			v.Status = "NeedsAttention"
			v.IconName = "grove-attention"
		}
	} else {
		lines = append(lines, "No completed attempt recorded")
	}
	if report.ProfileState == "busy" {
		lines = append(lines, "Profile details busy")
	} else if !fresh {
		lines = append(lines, "Profile details unavailable")
	}
	if m.facts != nil {
		prefix := ""
		if !fresh {
			prefix = "Last known "
		}
		label := "Conflicts"
		if prefix != "" {
			label = prefix + "conflicts"
		}
		lines = append(lines, fmt.Sprintf("%s: %d", label, m.facts.ConflictCount))
		if m.facts.RecoveryPending {
			if fresh {
				lines = append(lines, "Recovery pending")
			} else {
				lines = append(lines, "Last known recovery pending")
			}
		}
		if !m.facts.PopulationComplete {
			lines = append(lines, "Population incomplete")
		}
		if m.facts.ConflictCount > 0 || m.facts.RecoveryPending {
			v.Status = "NeedsAttention"
			v.IconName = "grove-attention"
			headline = "Needs attention"
		}
	}
	if sample.Err != nil || owner == "unknown" || report.ProfileState == "unavailable" || !fresh && m.facts == nil {
		headline = "Status unavailable"
		v.Status = "NeedsAttention"
		v.IconName = "grove-attention"
		if sample.Err != nil {
			lines = append(lines, "Retained information is historical")
		}
	}
	lines = append([]string{headline}, lines...)
	v.TooltipText = strings.Join(lines, "\n")
	v.Rows = []Row{{ID: 1, Label: opts.Name}, {ID: 2, Label: headline}}
	for i, line := range lines[1:] {
		v.Rows = append(v.Rows, Row{ID: int32(10 + i), Label: line})
	}
	v.Rows = append(v.Rows, Row{ID: 100, Label: "Refresh status", Enabled: true, Action: ActionRefresh})
	if service.Unit != "" {
		state := "Service: " + service.Phase
		if service.Reason != "" {
			state += "; " + service.Reason
		}
		v.Rows = append(v.Rows, Row{ID: 101, Label: state}, Row{ID: 102, Label: "Start " + opts.ServiceInstance + " service", Enabled: service.CanStart && owner == "inactive" && sample.Err == nil && fresh, Action: ActionStart}, Row{ID: 103, Label: "Stop " + opts.ServiceInstance + " service", Enabled: service.CanStop, Action: ActionStop})
	}
	v.Rows = append(v.Rows, Row{ID: 110, Separator: true}, Row{ID: 111, Label: "Quit tray", Enabled: true, Action: ActionQuit})
	return m, v
}
func safePhase(phase string) string {
	switch phase {
	case "starting", "syncing", "waiting", "stopping", "stopped":
		return phase
	default:
		return "unknown"
	}
}
func attemptLabel(outcome string) string {
	switch outcome {
	case "completed":
		return "Last sync completed"
	case "failed":
		return "Last sync failed"
	case "busy":
		return "Last sync busy"
	case "cancelled":
		return "Last sync cancelled"
	default:
		return "No completed attempt recorded"
	}
}
