package tray

import "sync"

func enqueueAction(actions chan Action, actionMu *sync.Mutex, action Action) {
	if action == ActionNone {
		return
	}
	actionMu.Lock()
	defer actionMu.Unlock()
	var pending []Action
	for {
		select {
		case a := <-actions:
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
		case actions <- a:
		default:
		}
	}
}
