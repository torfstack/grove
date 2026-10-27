package tray

import (
	"context"
	"errors"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const itemInterface = "org.kde.StatusNotifierItem"
const watcherName = "org.kde.StatusNotifierWatcher"
const menuInterface = "com.canonical.dbusmenu"
const itemPath dbus.ObjectPath = "/StatusNotifierItem"
const menuPath dbus.ObjectPath = "/MenuBar"

type Bus struct {
	conn      *dbus.Conn
	opts      Options
	mu        sync.RWMutex
	view      View
	revision  uint32
	actions   chan Action
	actionMu  sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

func OpenBus(ctx context.Context, opts Options) (*Bus, error) {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, errors.New("session bus unavailable")
	}
	return openBus(ctx, opts, conn)
}
func openBus(ctx context.Context, opts Options, conn *dbus.Conn) (b *Bus, err error) {
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	reply, err := conn.RequestName("org.grove.Tray.p"+Identity(opts.ProfileDir), dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, errors.New("tray already running or bus ownership unavailable")
	}
	life, cancel := context.WithCancel(ctx)
	b = &Bus{conn: conn, opts: opts, actions: make(chan Action, 4), cancel: cancel, view: View{Status: "NeedsAttention", IconName: "grove-attention", TooltipTitle: opts.Name, TooltipText: "Status unavailable"}, revision: 1}
	for _, obj := range []struct {
		value any
		path  dbus.ObjectPath
		iface string
	}{{&itemMethods{b}, itemPath, itemInterface}, {&menuMethods{b}, menuPath, menuInterface}, {&busProperties{b, itemPath}, itemPath, "org.freedesktop.DBus.Properties"}, {&busProperties{b, menuPath}, menuPath, "org.freedesktop.DBus.Properties"}} {
		if err = conn.Export(obj.value, obj.path, obj.iface); err != nil {
			cancel()
			return nil, errors.New("cannot export tray interfaces")
		}
	}
	if err = b.exportIntrospection(); err != nil {
		cancel()
		return nil, err
	}
	b.wg.Add(1)
	go b.watch(life)
	return b, nil
}
func (b *Bus) Actions() <-chan Action { return b.actions }
func (b *Bus) Done() <-chan struct{}  { return b.conn.Context().Done() }
func (b *Bus) Close() error {
	b.closeOnce.Do(func() { b.cancel(); b.closeErr = b.conn.Close(); b.wg.Wait() })
	return b.closeErr
}
func (b *Bus) enqueue(action Action) {
	if action == ActionNone {
		return
	}
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	var pending []Action
	for {
		select {
		case a := <-b.actions:
			pending = append(pending, a)
		default:
			goto drained
		}
	}
drained:
	found := false
	for _, a := range pending {
		if a == action {
			found = true
		}
	}
	if !found {
		pending = append(pending, action)
	}
	for _, a := range pending {
		select {
		case b.actions <- a:
		default:
		}
	}
}
func (b *Bus) Publish(view View) error {
	b.mu.Lock()
	old := b.view
	view.Rows = append([]Row(nil), view.Rows...)
	b.view = view
	menuChanged := !reflect.DeepEqual(old.Rows, view.Rows)
	if menuChanged {
		b.revision++
	}
	rev := b.revision
	b.mu.Unlock()
	before, after := b.itemProperties(old), b.itemProperties(view)
	changes := map[string]dbus.Variant{}
	for k, v := range after {
		if !reflect.DeepEqual(before[k], v) {
			changes[k] = dbus.MakeVariant(v)
		}
	}
	emit := func(path dbus.ObjectPath, name string, args ...any) error {
		if err := b.conn.Emit(path, name, args...); err != nil {
			return errors.New("cannot publish tray status")
		}
		return nil
	}
	if len(changes) > 0 {
		if err := emit(itemPath, "org.freedesktop.DBus.Properties.PropertiesChanged", itemInterface, changes, []string{}); err != nil {
			return err
		}
	}
	signals := []struct {
		changed bool
		name    string
		args    []any
	}{{old.IconName != view.IconName, "NewIcon", nil}, {old.Status != view.Status, "NewStatus", []any{view.Status}}, {old.TooltipTitle != view.TooltipTitle || old.TooltipText != view.TooltipText, "NewToolTip", nil}, {old.TooltipTitle != view.TooltipTitle, "NewTitle", nil}}
	for _, s := range signals {
		if s.changed {
			if err := emit(itemPath, itemInterface+"."+s.name, s.args...); err != nil {
				return err
			}
		}
	}
	if menuChanged {
		return emit(menuPath, menuInterface+".LayoutUpdated", rev, int32(0))
	}
	return nil
}
func (b *Bus) watch(ctx context.Context) {
	defer b.wg.Done()
	sigs := make(chan *dbus.Signal, 32)
	b.conn.Signal(sigs)
	defer b.conn.RemoveSignal(sigs)
	if err := b.conn.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, watcherName)); err != nil {
		return
	}
	owner := ""
	call := b.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, watcherName)
	_ = call.Store(&owner)
	type registration struct {
		owner string
		err   error
	}
	results := make(chan registration, 8)
	var cancel context.CancelFunc
	var retry <-chan time.Time
	var timer *time.Timer
	backoff := 200 * time.Millisecond
	start := func() {
		if owner == "" {
			return
		}
		requestCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		cancel = stop
		target := owner
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			err := b.conn.Object(target, "/StatusNotifierWatcher").CallWithContext(requestCtx, watcherName+".RegisterStatusNotifierItem", 0, string(itemPath)).Err
			stop()
			select {
			case results <- registration{target, err}:
			case <-ctx.Done():
			}
		}()
	}
	defer func() {
		if cancel != nil {
			cancel()
		}
		if timer != nil {
			timer.Stop()
		}
	}()
	if owner == "" {
		log.Print("grove-tray: waiting for a tray host")
	}
	start()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.Done():
			return
		case sig := <-sigs:
			if sig == nil || len(sig.Body) != 3 {
				continue
			}
			next, ok := sig.Body[2].(string)
			if !ok {
				continue
			}
			if cancel != nil {
				cancel()
			}
			if timer != nil {
				timer.Stop()
			}
			retry = nil
			owner = next
			backoff = 200 * time.Millisecond
			if owner == "" {
				log.Print("grove-tray: waiting for a tray host")
			}
			start()
		case r := <-results:
			if r.owner != owner {
				continue
			}
			if r.err != nil && owner != "" {
				timer = time.NewTimer(backoff)
				retry = timer.C
				backoff = min(backoff*2, 5*time.Second)
			}
		case <-retry:
			retry = nil
			start()
		}
	}
}
func (b *Bus) exportIntrospection() error {
	for _, p := range []dbus.ObjectPath{itemPath, menuPath} {
		name := itemInterface
		methods := introspect.Methods(&itemMethods{b})
		props := b.itemProperties(b.view)
		if p == menuPath {
			name = menuInterface
			methods = introspect.Methods(&menuMethods{b})
			props = menuPropertyValues()
		}
		iface := introspect.Interface{Name: name, Methods: methods}
		if p == itemPath {
			iface.Signals = []introspect.Signal{{Name: "NewIcon"}, {Name: "NewAttentionIcon"}, {Name: "NewToolTip"}, {Name: "NewTitle"}, {Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}}}
		} else {
			iface.Signals = []introspect.Signal{{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}}, {Name: "ItemsPropertiesUpdated", Args: []introspect.Arg{{Name: "updated", Type: "a(ia{sv})"}, {Name: "removed", Type: "a(ias)"}}}, {Name: "ItemActivationRequested", Args: []introspect.Arg{{Name: "id", Type: "i"}, {Name: "timestamp", Type: "u"}}}}
		}
		for k, v := range props {
			iface.Properties = append(iface.Properties, introspect.Property{Name: k, Type: dbus.SignatureOf(v).String(), Access: "read"})
		}
		node := &introspect.Node{Name: string(p), Interfaces: []introspect.Interface{iface, introspect.IntrospectData, introspect.PeerData, {Name: "org.freedesktop.DBus.Properties", Methods: introspect.Methods(&busProperties{b, p})}}}
		if err := b.conn.Export(introspect.NewIntrospectable(node), p, "org.freedesktop.DBus.Introspectable"); err != nil {
			return errors.New("cannot export tray introspection")
		}
	}
	return nil
}
