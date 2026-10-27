package tray

import (
	"bufio"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func NewTestBus(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1", "--nopidfile")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatalf("dbus-daemon prerequisite: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil && cmd.ProcessState == nil {
			t.Log(err)
		}
		_ = cmd.Wait()
	})
	line := make(chan string, 1)
	go func() { r := bufio.NewReader(out); s, _ := r.ReadString('\n'); line <- s }()
	select {
	case addr := <-line:
		if len(addr) < 2 {
			t.Fatal("missing bus address")
		}
		return addr[:len(addr)-1]
	case <-time.After(5 * time.Second):
		t.Fatal("bus startup timeout")
	}
	return ""
}
func testConn(t *testing.T, addr string) *dbus.Conn {
	t.Helper()
	c, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type fakeWatcher struct {
	registered chan string
	gate       <-chan struct{}
}

func (w *fakeWatcher) RegisterStatusNotifierItem(path string) *dbus.Error {
	w.registered <- path
	if w.gate != nil {
		<-w.gate
	}
	return nil
}
func watchOn(t *testing.T, c *dbus.Conn, gate <-chan struct{}) *fakeWatcher {
	t.Helper()
	w := &fakeWatcher{registered: make(chan string, 10), gate: gate}
	if err := c.Export(w, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	reply, err := c.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("watch name: %v %v", reply, err)
	}
	return w
}
func awaitRegistration(t *testing.T, w *fakeWatcher) {
	t.Helper()
	select {
	case path := <-w.registered:
		if path != "/StatusNotifierItem" {
			t.Fatalf("registered %q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no registration")
	}
}
