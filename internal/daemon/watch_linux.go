//go:build linux

package daemon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

type nativeWatcher struct {
	readEntries func(*os.File) ([]os.DirEntry, error)
	root        string
	rootFile    *os.File
	rootInfo    os.FileInfo
	file        *os.File
	fd          int
	clock       Clock
	mailbox     *watchMailbox
	ctx         context.Context
	cancel      context.CancelFunc
	repair      chan struct{}
	workers     sync.WaitGroup
	coverage    sync.Mutex
	watches     map[int]struct{}
	rootWD      atomic.Int64
	closeOnce   sync.Once
	closeErr    error
}

func localWatchSupported() bool { return true }

func openWatcher(ctx context.Context, root string, clock Clock) (Watcher, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, failed("watch_setup_failed", err)
	}
	rootFile := os.NewFile(uintptr(rootFD), "watch-root")
	info, err := rootFile.Stat()
	if err != nil {
		return nil, errors.Join(failed("watch_setup_failed", err), rootFile.Close())
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, errors.Join(failed("watch_setup_failed", err), rootFile.Close())
	}
	life, cancel := context.WithCancel(ctx)
	w := &nativeWatcher{root: root, rootFile: rootFile, rootInfo: info, file: os.NewFile(uintptr(fd), "watch-events"), fd: fd, clock: clock, mailbox: newWatchMailbox(), ctx: life, cancel: cancel, repair: make(chan struct{}, 1), watches: map[int]struct{}{}}
	// Start the reader before walking so registration cannot block event draining.
	w.workers.Add(1)
	go w.read()
	if err := w.Reconcile(ctx); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	w.workers.Add(1)
	go w.repairs()
	return w, nil
}
func (w *nativeWatcher) Ready() <-chan struct{} { return w.mailbox.ready }
func (w *nativeWatcher) Take() WatchBatch       { return w.mailbox.take() }
func (w *nativeWatcher) requestRepair() {
	select {
	case w.repair <- struct{}{}:
	default:
	}
}
func (w *nativeWatcher) rootValid() error {
	info, err := os.Lstat(w.root)
	if err != nil || !info.IsDir() || !os.SameFile(info, w.rootInfo) {
		return failed("watch_root_changed", err)
	}
	return nil
}
func (w *nativeWatcher) read() {
	defer w.workers.Done()
	buf := make([]byte, 64*1024)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			w.readError(err)
			return
		}
		if err := w.events(buf[:n]); err != nil {
			w.mailbox.fail(err)
			return
		}
	}
}
func (w *nativeWatcher) readError(err error) {
	if w.ctx.Err() == nil || !errors.Is(err, os.ErrClosed) {
		w.mailbox.fail(failed("watch_read_failed", err))
	}
}

func (w *nativeWatcher) events(data []byte) error {
	for len(data) > 0 {
		if len(data) < unix.SizeofInotifyEvent {
			return failed("watch_event_invalid", nil)
		}
		wd := int(int32(binary.NativeEndian.Uint32(data)))
		mask := binary.NativeEndian.Uint32(data[4:])
		size := int(binary.NativeEndian.Uint32(data[12:])) + unix.SizeofInotifyEvent
		if size > len(data) {
			return failed("watch_event_invalid", nil)
		}
		data = data[size:]
		if mask&unix.IN_UNMOUNT != 0 {
			return failed("watch_unmounted", nil)
		}
		rootWD := int(w.rootWD.Load())
		if wd == rootWD && mask&(unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 {
			return failed("watch_root_changed", nil)
		}
		if err := w.rootValid(); err != nil {
			return err
		}
		w.mailbox.hint(w.clock.Now())
		if mask&(unix.IN_Q_OVERFLOW|unix.IN_IGNORED|unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 || mask&unix.IN_ISDIR != 0 {
			w.requestRepair()
		}
	}
	return nil
}
func (w *nativeWatcher) repairs() {
	defer w.workers.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.repair:
			if err := w.Reconcile(w.ctx); err != nil {
				if w.ctx.Err() == nil || !cancellationOnly(err, context.Cause(w.ctx)) {
					w.mailbox.fail(err)
				}
				return
			}
		}
	}
}

var errWatchChanged = errors.New("watch membership changed")

func (w *nativeWatcher) Reconcile(ctx context.Context) error {
	w.coverage.Lock()
	defer w.coverage.Unlock()
	for attempt := 0; attempt < 8; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.rootValid(); err != nil {
			return err
		}
		next := map[int]struct{}{}
		fd, err := unix.Openat(int(w.rootFile.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return failed("watch_coverage_failed", err)
		}
		dir := os.NewFile(uintptr(fd), "watch-directory")
		err = w.walk(ctx, dir, next, true)
		err = errors.Join(err, dir.Close())
		if errors.Is(err, errWatchChanged) {
			w.mailbox.hint(w.clock.Now())
			continue
		}
		if err != nil {
			return failed("watch_coverage_failed", err)
		}
		if err := w.rootValid(); err != nil {
			return err
		}
		for wd := range w.watches {
			if _, ok := next[wd]; !ok {
				if _, err := unix.InotifyRmWatch(w.fd, uint32(wd)); err != nil && !errors.Is(err, unix.EINVAL) {
					return failed("watch_coverage_failed", err)
				}
			}
		}
		w.watches = next
		return nil
	}
	return failed("watch_coverage_unstable", nil)
}
func (w *nativeWatcher) walk(ctx context.Context, dir *os.File, next map[int]struct{}, root bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !w.withinRoot(int(dir.Fd())) {
		return errWatchChanged
	}
	const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_MOVE_SELF | unix.IN_DELETE_SELF | unix.IN_ONLYDIR
	wd, err := unix.InotifyAddWatch(w.fd, fmt.Sprintf("/proc/self/fd/%d", dir.Fd()), mask)
	if err != nil {
		return err
	}
	if root {
		w.rootWD.Store(int64(wd))
	}
	if _, ok := w.watches[wd]; !ok {
		w.mailbox.hint(w.clock.Now())
	}
	w.watches[wd] = struct{}{}
	next[wd] = struct{}{}
	read := w.readEntries
	if read == nil {
		read = func(f *os.File) ([]os.DirEntry, error) { return f.ReadDir(-1) }
	}
	entries, err := read(dir)
	if errors.Is(err, os.ErrNotExist) {
		return errWatchChanged
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fd, err := unix.Openat(int(dir.Fd()), entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return errWatchChanged
		}
		if err != nil {
			return err
		}
		child := os.NewFile(uintptr(fd), "watch-directory")
		err = w.walk(ctx, child, next, false)
		err = errors.Join(err, child.Close())
		if err != nil {
			return err
		}
	}
	if !w.withinRoot(int(dir.Fd())) {
		return errWatchChanged
	}
	return nil
}
func (w *nativeWatcher) withinRoot(fd int) bool {
	current, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	for depth := 0; depth < 4096; depth++ {
		f := os.NewFile(uintptr(current), "watch-ancestor")
		info, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return false
		}
		if os.SameFile(info, w.rootInfo) {
			_ = f.Close()
			return true
		}
		parent, openErr := unix.Openat(current, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		closeErr := f.Close()
		if openErr != nil {
			return false
		}
		if closeErr != nil {
			_ = unix.Close(parent)
			return false
		}
		parentFile := os.NewFile(uintptr(parent), "watch-ancestor")
		parentInfo, parentErr := parentFile.Stat()
		if parentErr != nil || os.SameFile(info, parentInfo) {
			_ = parentFile.Close()
			return false
		}
		// Transfer descriptor ownership to the next iteration.
		current, err = unix.Dup(parent)
		closeParentErr := parentFile.Close()
		if err != nil {
			return false
		}
		if closeParentErr != nil {
			_ = unix.Close(current)
			return false
		}
	}
	_ = unix.Close(current)
	return false
}
func (w *nativeWatcher) Close() error {
	w.closeOnce.Do(func() {
		w.cancel()
		err := w.file.Close()
		w.workers.Wait()
		w.coverage.Lock()
		defer w.coverage.Unlock()
		w.closeErr = errors.Join(err, w.rootFile.Close())
		if w.closeErr != nil {
			w.closeErr = failed("watch_cleanup_failed", w.closeErr)
		}
	})
	return w.closeErr
}

var _ io.Closer = (*nativeWatcher)(nil)
