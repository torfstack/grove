//go:build darwin

package tray

import (
	"testing"
	"time"
)

type fakeNativeItem struct {
	clicked  chan struct{}
	disabled bool
}

func (i *fakeNativeItem) Disable()                 { i.disabled = true }
func (i *fakeNativeItem) Clicked() <-chan struct{} { return i.clicked }

type fakeNativeMenu struct {
	labels     []string
	items      []*fakeNativeItem
	separators int
	icon       []byte
	tooltip    string
	resets     int
	quit       bool
}

func (m *fakeNativeMenu) ResetMenu() {
	m.resets++
	for _, item := range m.items {
		close(item.clicked)
	}
	m.labels = nil
	m.items = nil
	m.separators = 0
}
func (m *fakeNativeMenu) AddMenuItem(label, _ string) nativeMenuItem {
	i := &fakeNativeItem{clicked: make(chan struct{}, 1)}
	m.labels = append(m.labels, label)
	m.items = append(m.items, i)
	return i
}
func (m *fakeNativeMenu) AddSeparator()                  { m.separators++ }
func (m *fakeNativeMenu) SetTemplateIcon(icon, _ []byte) { m.icon = icon }
func (m *fakeNativeMenu) SetTooltip(v string)            { m.tooltip = v }
func (m *fakeNativeMenu) Quit()                          { m.quit = true }

func TestMenuBarPublishAndActionDispatch(t *testing.T) {
	menu := &fakeNativeMenu{}
	bus := &nativeBus{menu: menu, actions: make(chan Action, 4), done: make(chan struct{})}
	defer close(bus.done)
	view := View{IconName: "grove-idle", TooltipTitle: "Personal", TooltipText: "Ready", Rows: []Row{
		{Label: "Refresh", Enabled: true, Action: ActionRefresh},
		{Label: "Start", Enabled: false, Action: ActionStart},
		{Label: "Stop", Enabled: true, Action: ActionStop},
		{Separator: true},
		{Label: "Quit", Enabled: true, Action: ActionQuit},
	}}
	if err := bus.Publish(view); err != nil {
		t.Fatal(err)
	}
	if len(menu.labels) != 4 || menu.labels[0] != "Refresh" || menu.separators != 1 || !menu.items[1].disabled || menu.tooltip != "Personal: Ready" || len(menu.icon) == 0 {
		t.Fatalf("menu mapping: %+v", menu)
	}
	for i, want := range []Action{ActionRefresh, ActionStart, ActionStop, ActionQuit} {
		menu.items[i].clicked <- struct{}{}
		select {
		case got := <-bus.Actions():
			if got != want {
				t.Fatalf("action=%v want=%v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("action %v not dispatched", want)
		}
	}
	menu.items[0].clicked <- struct{}{}
	select {
	case got := <-bus.Actions():
		if got != ActionRefresh {
			t.Fatalf("second refresh action = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second refresh was not dispatched")
	}
	oldItem := menu.items[0]
	view.Rows = []Row{{Label: "Updated", Enabled: true}}
	if err := bus.Publish(view); err != nil || menu.resets != 2 || len(menu.labels) != 1 || menu.labels[0] != "Updated" {
		t.Fatalf("menu refresh: %v %+v", err, menu)
	}
	if _, open := <-oldItem.clicked; open {
		t.Fatal("removed native menu item remained open")
	}
	select {
	case action := <-bus.Actions():
		t.Fatalf("removed menu item dispatched stale action %v", action)
	case <-time.After(10 * time.Millisecond):
	}
}
