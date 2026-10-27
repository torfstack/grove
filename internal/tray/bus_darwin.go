//go:build darwin

package tray

import (
	"context"
	"errors"
	"sync"

	"fyne.io/systray"
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
	menu    nativeMenu
	actions chan Action
	done    chan struct{}
	cancel  context.CancelFunc
	once    sync.Once
	mu      sync.Mutex
	items   []nativeMenuItem
}

func OpenBus(ctx context.Context, _ Options) (TrayBus, error) {
	life, cancel := context.WithCancel(ctx)
	b := &nativeBus{menu: systrayMenu{}, actions: make(chan Action, 8), done: make(chan struct{}), cancel: cancel}
	go func() { <-life.Done(); close(b.done) }()
	return b, nil
}

func (b *nativeBus) Actions() <-chan Action { return b.actions }
func (b *nativeBus) Done() <-chan struct{}  { return b.done }
func (b *nativeBus) Close() error {
	b.once.Do(func() { b.cancel(); b.menu.Quit() })
	return nil
}

func (b *nativeBus) Publish(view View) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.menu.ResetMenu()
	b.items = b.items[:0]
	for _, row := range view.Rows {
		if row.Separator {
			b.menu.AddSeparator()
			continue
		}
		item := b.menu.AddMenuItem(row.Label, "")
		if !row.Enabled {
			item.Disable()
		}
		b.items = append(b.items, item)
		go func(item nativeMenuItem, action Action) {
			select {
			case <-item.Clicked():
				if action != ActionNone {
					select {
					case b.actions <- action:
					default:
					}
				}
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
