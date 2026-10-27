package tray

import "github.com/torfstack/grove/internal/daemon"

type Action uint8

const (
	ActionNone Action = iota
	ActionRefresh
	ActionStart
	ActionStop
	ActionQuit
)

type Sample struct {
	Status daemon.StatusReport
	Err    error
}
type ServiceState struct {
	Unit, Phase, Reason string
	CanStart, CanStop   bool
}
type Row struct {
	ID                 int32
	Label              string
	Enabled, Separator bool
	Action             Action
}
type View struct {
	Status, IconName, TooltipTitle, TooltipText string
	Rows                                        []Row
}
type Memory struct {
	profile string
	facts   *daemon.ProfileView
}
