package tray

import (
	"github.com/godbus/dbus/v5"
	"testing"
	"time"
)

func TestDBusMenuWire(t *testing.T) {
	b, _ := testBusItem(t)
	v := View{Status: "Active", IconName: "grove-idle", Rows: []Row{{ID: 1, Label: "Personal_name"}, {ID: 100, Label: "Refresh status", Enabled: true, Action: ActionRefresh}, {ID: 102, Label: "Start", Action: ActionStart}, {ID: 110, Separator: true}, {ID: 111, Label: "Quit tray", Enabled: true, Action: ActionQuit}}}
	if err := b.Publish(v); err != nil {
		t.Fatal(err)
	}
	obj := b.conn.Object(b.conn.Names()[0], "/MenuBar")
	var revision uint32
	var tree menuLayout
	if err := obj.Call("com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(-1), []string{}).Store(&revision, &tree); err != nil {
		t.Fatal(err)
	}
	if dbus.SignatureOf(tree).String() != "(ia{sv}av)" || len(tree.Children) != 5 {
		t.Fatalf("layout: %+v", tree)
	}
	var first menuLayout
	if err := dbus.Store([]any{tree.Children[0].Value()}, &first); err != nil {
		t.Fatal(err)
	}
	if first.Properties["label"].Value() != "Personal__name" {
		t.Fatalf("mnemonic not escaped: %+v", first)
	}
	if err := obj.Call("com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(0), []string{}).Store(&revision, &tree); err != nil || len(tree.Children) != 0 {
		t.Fatalf("depth: %+v %v", tree, err)
	}
	for _, id := range []int32{999, 102} {
		_ = obj.Call("com.canonical.dbusmenu.Event", 0, id, "clicked", dbus.MakeVariant(int32(0)), uint32(0))
	}
	select {
	case action := <-b.Actions():
		t.Fatalf("invalid event dispatched %v", action)
	default:
	}
	for i := 0; i < 3; i++ {
		if err := obj.Call("com.canonical.dbusmenu.Event", 0, int32(100), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case a := <-b.Actions():
		if a != ActionRefresh {
			t.Fatal(a)
		}
	case <-time.After(time.Second):
		t.Fatal("no refresh")
	}
	select {
	case <-b.Actions():
		t.Fatal("duplicate refresh not coalesced")
	default:
	}
	if obj.Call("com.canonical.dbusmenu.GetProperty", 0, int32(999), "label").Err == nil {
		t.Fatal("unknown row accepted")
	}
	var groups []menuProperties
	if err := obj.Call("com.canonical.dbusmenu.GetGroupProperties", 0, []int32{100}, []string{"enabled"}).Store(&groups); err != nil || len(groups) != 1 || len(groups[0].Properties) != 1 {
		t.Fatalf("group properties: %+v %v", groups, err)
	}
	var changed bool
	if err := obj.Call("com.canonical.dbusmenu.AboutToShow", 0, int32(0)).Store(&changed); err != nil {
		t.Fatal(err)
	}
}
func TestIconPixmaps(t *testing.T) {
	for _, name := range []string{"grove-idle", "grove-syncing", "grove-stopped", "grove-attention"} {
		pixels := iconPixmaps(name)
		if len(pixels) != 4 {
			t.Fatalf("%s no fallback", name)
		}
		for _, p := range pixels {
			if p.Width != p.Height || len(p.Data) != int(p.Width*p.Height*4) {
				t.Fatal("invalid pixmap")
			}
		}
	}
}

func TestDBusMenuGroupedEvents(t *testing.T) {
	b, obj := testBusItem(t)
	if err := b.Publish(View{Status: "Active", IconName: "grove-idle", Rows: []Row{{ID: 100, Label: "Refresh", Enabled: true, Action: ActionRefresh}, {ID: 111, Label: "Quit", Enabled: true, Action: ActionQuit}}}); err != nil {
		t.Fatal(err)
	}
	menu := b.conn.Object(b.conn.Names()[0], menuPath)
	var failed []int32
	events := []menuEvent{{100, "clicked", dbus.MakeVariant(int32(0)), 0}, {999, "clicked", dbus.MakeVariant(int32(0)), 0}, {111, "clicked", dbus.MakeVariant(int32(0)), 0}}
	if err := menu.Call(menuInterface+".EventGroup", 0, events).Store(&failed); err != nil || len(failed) != 1 || failed[0] != 999 {
		t.Fatalf("grouped events: %v %v", failed, err)
	}
	if <-b.Actions() != ActionRefresh || <-b.Actions() != ActionQuit {
		t.Fatal("event order changed")
	}
	var updates, bad []int32
	if err := menu.Call(menuInterface+".AboutToShowGroup", 0, []int32{0, 999}).Store(&updates, &bad); err != nil || len(updates) != 0 || len(bad) != 1 || bad[0] != 999 {
		t.Fatalf("show group: %v %v %v", updates, bad, err)
	}
	<-b.Actions()
	if err := obj.Call(itemInterface+".Activate", 0, int32(0), int32(0)).Err; err != nil {
		t.Fatal(err)
	}
	if <-b.Actions() != ActionRefresh {
		t.Fatal("activate must not change services")
	}
}

func TestDBusMenuRootProperties(t *testing.T) {
	b, _ := testBusItem(t)
	obj := b.conn.Object(b.conn.Names()[0], menuPath)
	var value dbus.Variant
	if err := obj.Call(menuInterface+".GetProperty", 0, int32(0), "children-display").Store(&value); err != nil || value.Value() != "submenu" {
		t.Fatalf("host cannot inspect menu root: %v %v", value, err)
	}
	var groups []menuProperties
	if err := obj.Call(menuInterface+".GetGroupProperties", 0, []int32{0}, []string{"children-display"}).Store(&groups); err != nil || len(groups) != 1 || groups[0].ID != 0 {
		t.Fatalf("root group: %+v %v", groups, err)
	}
}
