package tray

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestSNIRegistration(t *testing.T) {
	addr := NewTestBus(t)
	ctx := testContext(t)
	wc := testConn(t, addr)
	w := watchOn(t, wc, nil)
	opts := Options{ProfileDir: "/private/one", Name: "Personal"}
	b, err := openBus(ctx, opts, testConn(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	awaitRegistration(t, w)
	if _, err := openBus(ctx, opts, testConn(t, addr)); err == nil {
		t.Fatal("duplicate accepted")
	}
	other, err := openBus(ctx, Options{ProfileDir: "/private/two", Name: "Personal"}, testConn(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	w = watchOn(t, testConn(t, addr), nil)
	awaitRegistration(t, w)
	if err := b.conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("lost bus not observed")
	}
}
func TestSNIWatcherReplacement(t *testing.T) {
	addr := NewTestBus(t)
	old := testConn(t, addr)
	gate := make(chan struct{})
	w := watchOn(t, old, gate)
	b, err := openBus(testContext(t), Options{ProfileDir: "/one"}, testConn(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	awaitRegistration(t, w)
	if _, err := old.ReleaseName("org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	fresh := watchOn(t, testConn(t, addr), nil)
	awaitRegistration(t, fresh)
	close(gate)
}
func testBusItem(t *testing.T) (*Bus, dbus.BusObject) {
	t.Helper()
	addr := NewTestBus(t)
	ctx := testContext(t)
	b, err := openBus(ctx, Options{ProfileDir: "/profile", Name: "Grove"}, testConn(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, testConn(t, addr).Object("org.grove.Tray.p"+Identity("/profile"), "/StatusNotifierItem")
}
func TestSNIPropertiesAndSignals(t *testing.T) {
	b, obj := testBusItem(t)
	_, v := Reduce(Memory{}, healthy(), ServiceState{}, Options{Name: "<Personal_>", ProfileDir: "/profile"})
	if err := b.Publish(v); err != nil {
		t.Fatal(err)
	}
	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, "org.kde.StatusNotifierItem").Store(&props); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"Category": "ApplicationStatus", "Status": "Active", "Title": "<Personal_>", "ItemIsMenu": true, "Menu": dbus.ObjectPath("/MenuBar"), "WindowId": uint32(0)} {
		if !reflect.DeepEqual(props[key].Value(), want) {
			t.Fatalf("%s=%v", key, props[key])
		}
	}
	if !strings.Contains(props["ToolTip"].String(), "&lt;Personal_&gt;") {
		t.Fatalf("tooltip title not escaped: %v", props["ToolTip"])
	}
	if obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.kde.StatusNotifierItem", "Status", dbus.MakeVariant("Passive")).Err == nil {
		t.Fatal("writable status")
	}
	if obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierItem", "private").Err == nil {
		t.Fatal("unknown property accepted")
	}
	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil || !strings.Contains(xml, "IconPixmap") {
		t.Fatalf("introspection: %v %s", err, xml)
	}
	sigs := make(chan *dbus.Signal, 20)
	b.conn.Signal(sigs)
	if err := b.conn.AddMatchSignal(dbus.WithMatchInterface("org.kde.StatusNotifierItem")); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(v); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-sigs:
		t.Fatalf("unchanged emitted %s", sig.Name)
	case <-time.After(20 * time.Millisecond):
	}
	v.Status = "NeedsAttention"
	v.IconName = "grove-attention"
	if err := b.Publish(v); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-sigs:
		if !strings.HasPrefix(sig.Name, "org.kde.StatusNotifierItem.") {
			t.Fatal(sig.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("no update signal")
	}
	b.conn.RemoveSignal(sigs)
}
func TestSNINoWatcher(t *testing.T) {
	addr := NewTestBus(t)
	b, err := openBus(context.Background(), Options{ProfileDir: "/profile"}, testConn(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
}

func TestSNIIntrospectionSignals(t *testing.T) {
	b, obj := testBusItem(t)
	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"NewIcon", "NewStatus", "NewToolTip"} {
		if !strings.Contains(xml, `<signal name="`+name+`"`) {
			t.Fatalf("signal missing from introspection: %s", name)
		}
	}
	menu := b.conn.Object(b.conn.Names()[0], menuPath)
	if err := menu.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `<signal name="LayoutUpdated"`) {
		t.Fatal("menu update signal missing")
	}
}
