package tray

import (
	"github.com/godbus/dbus/v5"
	"strings"
)

type menuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}
type menuProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}
type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}
type menuMethods struct{ bus *Bus }

func menuPropertyValues() map[string]any {
	return map[string]any{"Version": uint32(3), "Status": "normal", "TextDirection": "ltr", "IconThemePath": []string{}}
}
func rowProperties(row Row, names []string) map[string]dbus.Variant {
	props := map[string]any{"label": strings.ReplaceAll(row.Label, "_", "__"), "enabled": row.Enabled, "visible": true}
	if row.Separator {
		props = map[string]any{"type": "separator", "visible": true}
	}
	result := map[string]dbus.Variant{}
	for k, v := range props {
		if len(names) == 0 {
			result[k] = dbus.MakeVariant(v)
			continue
		}
		for _, name := range names {
			if name == k {
				result[k] = dbus.MakeVariant(v)
			}
		}
	}
	return result
}
func (m *menuMethods) GetLayout(parent, depth int32, names []string) (uint32, menuLayout, *dbus.Error) {
	m.bus.mu.RLock()
	defer m.bus.mu.RUnlock()
	root := menuLayout{ID: parent, Properties: map[string]dbus.Variant{}, Children: []dbus.Variant{}}
	if parent == 0 {
		if len(names) == 0 {
			root.Properties["children-display"] = dbus.MakeVariant("submenu")
		} else {
			for _, name := range names {
				if name == "children-display" {
					root.Properties[name] = dbus.MakeVariant("submenu")
				}
			}
		}
		if depth != 0 {
			for _, row := range m.bus.view.Rows {
				root.Children = append(root.Children, dbus.MakeVariant(menuLayout{row.ID, rowProperties(row, names), []dbus.Variant{}}))
			}
		}
		return m.bus.revision, root, nil
	}
	for _, row := range m.bus.view.Rows {
		if row.ID == parent {
			root.Properties = rowProperties(row, names)
			return m.bus.revision, root, nil
		}
	}
	return 0, menuLayout{}, invalid("unknown menu item")
}
func (m *menuMethods) GetGroupProperties(ids []int32, names []string) ([]menuProperties, *dbus.Error) {
	m.bus.mu.RLock()
	defer m.bus.mu.RUnlock()
	result := []menuProperties{}
	for _, row := range m.bus.view.Rows {
		include := len(ids) == 0
		for _, id := range ids {
			if id == row.ID {
				include = true
			}
		}
		if include {
			result = append(result, menuProperties{row.ID, rowProperties(row, names)})
		}
	}
	return result, nil
}
func (m *menuMethods) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	groups, _ := m.GetGroupProperties([]int32{id}, []string{name})
	for _, row := range groups {
		if value, ok := row.Properties[name]; ok {
			return value, nil
		}
	}
	return dbus.Variant{}, invalid("unknown menu item or property")
}
func (m *menuMethods) Event(id int32, event string, _ dbus.Variant, _ uint32) *dbus.Error {
	m.bus.mu.RLock()
	action := ActionNone
	found := false
	for _, row := range m.bus.view.Rows {
		if row.ID == id {
			found = true
			if row.Enabled && event == "clicked" {
				action = row.Action
			}
			break
		}
	}
	m.bus.mu.RUnlock()
	if !found {
		return invalid("unknown menu item")
	}
	m.bus.enqueue(action)
	return nil
}
func (m *menuMethods) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	failed := []int32{}
	for _, e := range events {
		if m.Event(e.ID, e.EventID, e.Data, e.Timestamp) != nil {
			failed = append(failed, e.ID)
		}
	}
	return failed, nil
}
func (m *menuMethods) AboutToShow(id int32) (bool, *dbus.Error) {
	if id != 0 {
		if _, err := m.GetProperty(id, "visible"); err != nil {
			return false, err
		}
	}
	m.bus.enqueue(ActionRefresh)
	return false, nil
}
func (m *menuMethods) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	updates, failed := []int32{}, []int32{}
	for _, id := range ids {
		if _, err := m.AboutToShow(id); err != nil {
			failed = append(failed, id)
		}
	}
	return updates, failed, nil
}
