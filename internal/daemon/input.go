package daemon

import (
	"encoding/binary"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const evKey = 0x01 // EV_KEY event type (linux/input-event-codes.h)

// WaitResult reports why WaitForKeyPress returned.
type WaitResult int

const (
	// WaitTimeout means the requested duration elapsed with no keyboard
	// activity at all.
	WaitTimeout WaitResult = iota
	// WaitKeyPress means a key-down event was seen on some watched device.
	WaitKeyPress
	// WaitCancelled means Cancel was called - the daemon is shutting down.
	WaitCancelled
)

// InputWatcher waits for keyboard activity across every readable
// /dev/input/eventN device using epoll, so it costs essentially nothing
// while idle - the calling goroutine blocks in the kernel until an event
// arrives rather than polling. Reading straight from these device nodes
// also happens to be why nodematrix never needs to explicitly "swallow"
// the keypress that dismisses it: it's reading input directly, in parallel
// with (not instead of) whatever the currently-active VT's own line
// discipline is doing, and while the login VT is inactive it structurally
// never receives that keystroke at all - this was validated on real target
// hardware by cmd/vtspike.
//
// It currently treats a key-down event from any input device as activity,
// rather than filtering out non-keyboard devices (mice, power buttons,
// etc.) - a reasonable simplification for a server console where such
// distinctions rarely matter in practice.
//
// Device fds are tracked as plain ints and read via unix.Read, not through
// os.File: Go's os.File.Read on a fd the runtime can poll (which evdev
// devices are) does not return EAGAIN the way a raw non-blocking read()
// does - the runtime instead parks the calling goroutine via its own
// internal poller until more data arrives, completely bypassing our own
// epoll set and its cancellation eventfd. That caused a real hang: after
// draining the last available event, the next read silently blocked
// forever waiting for that specific device to produce more data, which
// never happened, and Cancel()'s eventfd write had no way to reach a
// goroutine blocked in a different, unrelated wait mechanism. unix.Read
// against the raw fd has none of that hidden behavior.
type InputWatcher struct {
	epfd     int
	fds      map[int]struct{}
	cancelFD int // eventfd; writing to it wakes any in-progress WaitForKeyPress for shutdown
}

// NewInputWatcher opens every /dev/input/eventN device it can and starts
// watching them all. Devices it lacks permission for are skipped rather
// than treated as fatal, since a subset of devices being unreadable is
// common and not necessarily a problem.
func NewInputWatcher() (*InputWatcher, error) {
	epfd, err := unix.EpollCreate1(0)
	if err != nil {
		return nil, fmt.Errorf("epoll_create1: %w", err)
	}
	w := &InputWatcher{epfd: epfd, fds: make(map[int]struct{})}

	cancelFD, err := unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC)
	if err != nil {
		unix.Close(epfd)
		return nil, fmt.Errorf("eventfd: %w", err)
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, cancelFD, &unix.EpollEvent{Events: unix.EPOLLIN, Fd: int32(cancelFD)}); err != nil {
		unix.Close(cancelFD)
		unix.Close(epfd)
		return nil, fmt.Errorf("registering cancel eventfd: %w", err)
	}
	w.cancelFD = cancelFD

	matches, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		w.Close()
		return nil, err
	}
	for _, path := range matches {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			continue
		}
		if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN, Fd: int32(fd)}); err != nil {
			unix.Close(fd)
			continue
		}
		w.fds[fd] = struct{}{}
	}
	if len(w.fds) == 0 {
		w.Close()
		return nil, fmt.Errorf("no readable /dev/input/event* devices found")
	}
	log.Printf("[input] watching %d device(s)", len(w.fds))
	return w, nil
}

// WaitForKeyPress blocks until a key-down event arrives on any watched
// device, Cancel is called, or timeout elapses (a negative timeout waits
// indefinitely).
func (w *InputWatcher) WaitForKeyPress(timeout time.Duration) (WaitResult, error) {
	var deadline time.Time
	hasDeadline := timeout >= 0
	if hasDeadline {
		deadline = time.Now().Add(timeout)
	}

	events := make([]unix.EpollEvent, len(w.fds)+1)
	for {
		ms := -1
		if hasDeadline {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return WaitTimeout, nil
			}
			ms = int(remaining.Milliseconds())
			if ms == 0 {
				ms = 1
			}
		}

		n, err := unix.EpollWait(w.epfd, events, ms)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return WaitTimeout, fmt.Errorf("epoll_wait: %w", err)
		}
		if n == 0 {
			return WaitTimeout, nil
		}

		sawKeyPress := false
		for _, ev := range events[:n] {
			if int(ev.Fd) == w.cancelFD {
				return WaitCancelled, nil
			}
			if _, ok := w.fds[int(ev.Fd)]; ok && drainKeyPresses(int(ev.Fd)) {
				sawKeyPress = true
			}
		}
		if sawKeyPress {
			return WaitKeyPress, nil
		}
		// Woke for a non-key event (EV_SYN, mouse motion, etc.) - loop
		// again with whatever time budget remains.
	}
}

// Cancel wakes any in-progress or future WaitForKeyPress call, causing it
// to return WaitCancelled. Used for graceful shutdown.
func (w *InputWatcher) Cancel() error {
	var one [8]byte
	binary.LittleEndian.PutUint64(one[:], 1)
	_, err := unix.Write(w.cancelFD, one[:])
	return err
}

// maxEventsPerDrain bounds how many events drainKeyPresses will read in one
// call, so a device producing a continuous stream of events (e.g. a stuck
// key whose release was dropped somewhere along a remote/virtual-KVM
// keyboard path, which the kernel's own autorepeat can then fire for
// indefinitely) can't keep this loop from returning - the caller's outer
// loop still gets a chance to check its deadline and cancellation between
// batches.
const maxEventsPerDrain = 64

// drainKeyPresses reads up to maxEventsPerDrain currently-buffered events
// from a non-blocking input device fd (via the raw read syscall - see the
// InputWatcher doc comment for why not os.File) and reports whether any of
// them was a key-down.
func drainKeyPresses(fd int) bool {
	var buf [24]byte // sizeof(struct input_event) on amd64/arm64
	saw := false
	for i := 0; i < maxEventsPerDrain; i++ {
		n, err := unix.Read(fd, buf[:])
		if err != nil || n < len(buf) {
			break
		}
		eventType := binary.LittleEndian.Uint16(buf[16:18])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if eventType == evKey && value == 1 {
			saw = true
		}
	}
	return saw
}

// Close stops watching and releases all device handles.
func (w *InputWatcher) Close() error {
	var firstErr error
	for fd := range w.fds {
		if err := unix.Close(fd); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := unix.Close(w.cancelFD); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := unix.Close(w.epfd); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
