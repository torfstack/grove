package tray

import (
	"github.com/godbus/dbus/v5"
	"html"
)

type tooltip struct {
	IconName           string
	IconPixmap         []Pixmap
	Title, Description string
}
type itemMethods struct{ bus *Bus }

func (i *itemMethods) Activate(int32, int32) *dbus.Error          { i.bus.enqueue(ActionRefresh); return nil }
func (i *itemMethods) SecondaryActivate(int32, int32) *dbus.Error { return nil }
func (i *itemMethods) ContextMenu(int32, int32) *dbus.Error       { return nil }
func (i *itemMethods) Scroll(int32, string) *dbus.Error           { return nil }
func (b *Bus) itemProperties(v View) map[string]any {
	return map[string]any{"Category": "ApplicationStatus", "Id": "grove-" + Identity(b.opts.ProfileDir), "Title": v.TooltipTitle, "Status": v.Status, "WindowId": uint32(0), "IconName": v.IconName, "IconPixmap": iconPixmaps(v.IconName), "OverlayIconName": "", "OverlayIconPixmap": []Pixmap{}, "AttentionIconName": "grove-attention", "AttentionIconPixmap": iconPixmaps("grove-attention"), "AttentionMovieName": "", "ToolTip": tooltip{v.IconName, []Pixmap{}, html.EscapeString(v.TooltipTitle), html.EscapeString(v.TooltipText)}, "ItemIsMenu": true, "Menu": menuPath}
}

type busProperties struct {
	bus  *Bus
	path dbus.ObjectPath
}

func (p *busProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	p.bus.mu.RLock()
	defer p.bus.mu.RUnlock()
	var values map[string]any
	if p.path == itemPath && iface == itemInterface {
		values = p.bus.itemProperties(p.bus.view)
	} else if p.path == menuPath && iface == menuInterface {
		values = menuPropertyValues()
	} else {
		return nil, invalid("unknown interface")
	}
	result := make(map[string]dbus.Variant, len(values))
	for k, v := range values {
		result[k] = dbus.MakeVariant(v)
	}
	return result, nil
}
func (p *busProperties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	props, err := p.GetAll(iface)
	if err != nil {
		return dbus.Variant{}, err
	}
	v, ok := props[name]
	if !ok {
		return dbus.Variant{}, invalid("unknown property")
	}
	return v, nil
}
func (p *busProperties) Set(string, string, dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", nil)
}
func invalid(message string) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{message})
}
