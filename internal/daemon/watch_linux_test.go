//go:build linux

package daemon

import (
	"context"
	"encoding/binary"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func watchFixture(t *testing.T) (string, Watcher) {
	t.Helper()
	root := t.TempDir()
	w, err := openWatcher(context.Background(), root, realClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	_ = w.Take()
	return root, w
}
func awaitWatch(t *testing.T, w Watcher) WatchBatch {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-w.Ready():
			b := w.Take()
			if b.Err != nil || !b.First.IsZero() {
				return b
			}
		case <-deadline.C:
			t.Fatal("missing hint")
			return WatchBatch{}
		}
	}
}
func TestWatchNestedAndAtomicSave(t *testing.T) {
	root, w := watchFixture(t)
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = w.Take()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if b := awaitWatch(t, w); b.Err != nil {
		t.Fatal(b.Err)
	}
	temp := filepath.Join(dir, "save")
	if err := os.WriteFile(temp, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, path); err != nil {
		t.Fatal(err)
	}
	if b := awaitWatch(t, w); b.Err != nil {
		t.Fatal(b.Err)
	}
}
func TestWatchMovedInTree(t *testing.T) {
	root, w := watchFixture(t)
	outside := t.TempDir()
	dir := filepath.Join(outside, "tree")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, filepath.Join(root, "tree")); err != nil {
		t.Fatal(err)
	}
	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = w.Take()
	if err := os.WriteFile(filepath.Join(root, "tree", "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if b := awaitWatch(t, w); b.Err != nil {
		t.Fatal(b.Err)
	}
}
func TestWatchRemovalAndMetadata(t *testing.T) {
	root, w := watchFixture(t)
	p := filepath.Join(root, "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = awaitWatch(t, w)
	if err := os.Chmod(p, 0640); err != nil {
		t.Fatal(err)
	}
	_ = awaitWatch(t, w)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	_ = awaitWatch(t, w)
}
func TestWatchReadDoesNotNotify(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := openWatcher(context.Background(), root, realClock{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	_ = w.Take()
	if _, err := os.ReadFile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadDir(root); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Ready():
		if b := w.Take(); !b.First.IsZero() || b.Err != nil {
			t.Fatal("reads caused hint", b)
		}
	case <-time.After(100 * time.Millisecond):
	}
}
func TestWatchRootIdentity(t *testing.T) {
	root, w := watchFixture(t)
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root + "-moved") }()
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.Reconcile(context.Background()); err == nil {
		t.Fatal("root replacement accepted")
	}
}
func TestWatchSymlinkRegistrationRace(t *testing.T) {
	root, w := watchFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := w.Reconcile(context.Background()); err != nil {
        t.Fatal(err)
    }
    native := w.(*nativeWatcher)
    child := filepath.Join(root, "race")
    if err := os.Mkdir(child,0700); err != nil { t.Fatal(err) }
    swapped := false
    native.coverage.Lock()
    native.readEntries = func(f *os.File)([]os.DirEntry,error) {
        entries,err := f.ReadDir(-1)
        if !swapped {
            swapped = true
            if renameErr := os.Rename(child,filepath.Join(outside,"moved")); renameErr != nil { return nil,renameErr }
            if linkErr := os.Symlink(outside,child); linkErr != nil { return nil,linkErr }
        }
        return entries,err
    }
    native.coverage.Unlock()
    if err := w.Reconcile(context.Background()); err != nil { t.Fatal(err) }
	// A barrier drains kernel events before asserting that outside writes are ignored.
	time.Sleep(50 * time.Millisecond)
	_ = w.Take()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Ready():
		if b := w.Take(); !b.First.IsZero() {
			t.Fatal("followed symlink")
		}
	case <-time.After(100 * time.Millisecond):
	}
}
func TestWatchSetupFailure(t *testing.T) {
	if w, err := openWatcher(context.Background(), filepath.Join(t.TempDir(), "absent"), realClock{}); err == nil {
		_ = w.Close()
		t.Fatal("absent root accepted")
	}
}
func TestWatchCloseUnblocks(t *testing.T) {
	_, w := watchFixture(t)
	done := make(chan error, 1)
	go func() { done <- w.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader leaked")
	}
}
func TestWatchMailbox(t *testing.T) {
	m := newWatchMailbox()
	first := time.Now()
	for i := 0; i < 1000000; i++ {
		m.hint(first.Add(time.Duration(i)))
	}
	b := m.take()
	if b.First != first || b.Last != first.Add(999999) {
		t.Fatal(b)
	}
	if !m.take().First.IsZero() {
		t.Fatal("not drained")
	}
}

func TestWatchOverflowRepair(t *testing.T) {
	root, watch := watchFixture(t)
	w := watch.(*nativeWatcher)
	dir := filepath.Join(root, "nested")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	event := make([]byte, 16)
	binary.NativeEndian.PutUint32(event, 0xffffffff)
	binary.NativeEndian.PutUint32(event[4:], unix.IN_Q_OVERFLOW)
	if err := w.events(event); err != nil {
		t.Fatal(err)
	}
	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = w.Take()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if b := awaitWatch(t, w); b.Err != nil {
		t.Fatal(b.Err)
	}
}
func TestWatchDescriptorReuse(t *testing.T) {
	root, watch := watchFixture(t)
	w := watch.(*nativeWatcher)
	for i := 0; i < 30; i++ {
		dir := filepath.Join(root, "directory")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := w.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if err := w.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "after"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if b := awaitWatch(t, w); b.Err != nil {
		t.Fatal(b.Err)
	}
}

func TestWatchRemovedDuringEnumeration(t *testing.T) {
	_, watch := watchFixture(t)
	w := watch.(*nativeWatcher)
	w.coverage.Lock()
	calls := 0
	w.readEntries = func(f *os.File) ([]os.DirEntry, error) {
		calls++
		if calls == 1 {
			return nil, os.ErrNotExist
		}
		return f.ReadDir(-1)
	}
	w.coverage.Unlock()
	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatal("transient directory removal stopped coverage repair", err)
	}
	if calls < 2 {
		t.Fatal("incomplete enumeration accepted")
	}
}
