package tray

import (
	"sync"
	"testing"
)

func TestActionQueueCoalescesAndPreservesQuit(t *testing.T) {
	actions := make(chan Action, 4)
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		enqueueAction(actions, &mu, ActionRefresh)
	}
	for _, action := range []Action{ActionStart, ActionStop, ActionQuit} {
		enqueueAction(actions, &mu, action)
	}
	seen := map[Action]int{}
	for len(actions) > 0 {
		seen[<-actions]++
	}
	for _, action := range []Action{ActionRefresh, ActionStart, ActionStop, ActionQuit} {
		if seen[action] != 1 {
			t.Fatalf("action %v occurred %d times", action, seen[action])
		}
	}
}
