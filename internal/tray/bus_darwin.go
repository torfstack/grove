//go:build darwin

package tray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/systray"
	"github.com/torfstack/grove/internal/privatefs"
)

type nativeMenu interface {
	ResetMenu()
	AddMenuItem(string, string) nativeMenuItem
	AddSeparator()
	SetTemplateIcon([]byte, []byte)
	SetTooltip(string)
	Quit()
}

type nativeMenuItem interface {
	Disable()
	Clicked() <-chan struct{}
}

type systrayMenu struct{}

func (systrayMenu) ResetMenu() { systray.ResetMenu() }
func (systrayMenu) AddMenuItem(label, tip string) nativeMenuItem {
	return systrayMenuItem{systray.AddMenuItem(label, tip)}
}
func (systrayMenu) AddSeparator()               { systray.AddSeparator() }
func (systrayMenu) SetTemplateIcon(b, _ []byte) { systray.SetTemplateIcon(b, b) }
func (systrayMenu) SetTooltip(v string)         { systray.SetTooltip(v) }
func (systrayMenu) Quit()                       { systray.Quit() }

type systrayMenuItem struct{ item *systray.MenuItem }

func (i systrayMenuItem) Disable()                 { i.item.Disable() }
func (i systrayMenuItem) Clicked() <-chan struct{} { return i.item.ClickedCh }

type nativeBus struct {
	menu       nativeMenu
	actions    chan Action
	done       chan struct{}
	cancel     context.CancelFunc
	once       sync.Once
	mu         sync.Mutex
	lock       *privatefs.Lock
	itemCancel context.CancelFunc
}

func OpenBus(ctx context.Context, opts Options) (TrayBus, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || privatefs.OutsideGit(cacheDir) != nil {
		return nil, errors.New("tray runtime directory unavailable")
	}
	lock, err := privatefs.Acquire(filepath.Join(cacheDir, "grove", "tray-"+Identity(opts.ProfileDir)+".lock"))
	if err != nil {
		return nil, errors.New("tray already running or identity lock unavailable")
	}
	life, cancel := context.WithCancel(ctx)
	b := &nativeBus{menu: systrayMenu{}, actions: make(chan Action, 8), done: make(chan struct{}), cancel: cancel, lock: lock}
	go func() { <-life.Done(); close(b.done) }()
	return b, nil
}

func (b *nativeBus) Actions() <-chan Action { return b.actions }
func (b *nativeBus) Done() <-chan struct{}  { return b.done }
func (b *nativeBus) Close() error {
	var err error
	b.once.Do(func() {
		b.cancel()
		b.mu.Lock()
		if b.itemCancel != nil {
			b.itemCancel()
			b.itemCancel = nil
		}
		b.mu.Unlock()
		b.menu.Quit()
		if b.lock != nil {
			err = b.lock.Close()
		}
	})
	return err
}

func (b *nativeBus) Publish(view View) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.itemCancel != nil {
		b.itemCancel()
	}
	itemCtx, itemCancel := context.WithCancel(context.Background())
	b.itemCancel = itemCancel
	b.menu.ResetMenu()
	for _, row := range view.Rows {
		if row.Separator {
			b.menu.AddSeparator()
			continue
		}
		item := b.menu.AddMenuItem(row.Label, "")
		if !row.Enabled {
			item.Disable()
		}
		go func(item nativeMenuItem, action Action) {
			select {
			case _, ok := <-item.Clicked():
				if !ok {
					return
				}
				if action != ActionNone {
					select {
					case b.actions <- action:
					default:
					}
				}
			case <-itemCtx.Done():
			case <-b.done:
			}
		}(item, row.Action)
	}
	b.menu.SetTemplateIcon(iconFor(view.IconName), iconFor(view.IconName))
	b.menu.SetTooltip(view.TooltipTitle + ": " + view.TooltipText)
	if len(view.Rows) == 0 {
		return errors.New("empty tray menu")
	}
	return nil
}
