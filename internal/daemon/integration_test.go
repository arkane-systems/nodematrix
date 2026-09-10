package daemon

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This file holds a real-hardware integration test for Manager: it requires
// root and a genuine kernel VT subsystem (VT_ACTIVATE, /dev/uinput), so it
// skips itself when those aren't available rather than failing - this is
// what lets `go test ./...` stay green in environments without a VT (e.g.
// most CI). The uinput helpers here duplicate a small amount of logic from
// cmd/vtspike (which validates the same underlying kernel primitives as a
// standalone diagnostic) rather than sharing code across an internal
// package and a throwaway cmd - acceptable for this much test-only code.

const testKeyCode = 44 // KEY_Z

// TestManagerShowHideAndDismiss exercises the exact mechanism behind the
// real-hardware bug this was written to catch: a keypress arriving on the
// screensaver's VT (via a synthetic keyboard, so no physical typing is
// needed) must be detected by WaitForDismiss, with no spurious dismissal
// beforehand.
func TestManagerShowHideAndDismiss(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := os.Stat("/dev/tty0"); err != nil {
		t.Skip("no /dev/tty0 - no VT subsystem available in this environment")
	}

	mgr, err := NewManager(63, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	defer mgr.Hide()

	if err := mgr.PrepareScreensaverTTY(100 * time.Millisecond); err != nil {
		t.Fatalf("PrepareScreensaverTTY: %v", err)
	}
	if err := mgr.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}

	dismissed, err := mgr.WaitForDismiss()
	if err != nil {
		t.Fatalf("WaitForDismiss (before injection): %v", err)
	}
	if dismissed {
		t.Fatal("WaitForDismiss reported a keypress before any was sent")
	}

	uinputFd, err := createVirtualKeyboard(testKeyCode)
	if err != nil {
		t.Skipf("could not create virtual keyboard via uinput (is the module loaded? try: modprobe uinput): %v", err)
	}
	if err := injectKeyPress(uinputFd, testKeyCode); err != nil {
		t.Fatalf("injectKeyPress: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := destroyVirtualKeyboard(uinputFd); err != nil {
		t.Fatalf("destroyVirtualKeyboard: %v", err)
	}

	dismissed, err = mgr.WaitForDismiss()
	if err != nil {
		t.Fatalf("WaitForDismiss (after injection): %v", err)
	}
	if !dismissed {
		t.Fatal("WaitForDismiss did not report the injected keypress")
	}

	if err := mgr.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
}

// TestInputWatcherSecondWaitTimesOutAfterDrainingEvents is the regression
// test for a real-hardware hang: after draining all currently-available
// events from a device, a second WaitForKeyPress call with no further input
// must time out - it must not block forever. It used to: os.File.Read on a
// pollable non-blocking fd doesn't return EAGAIN the way a raw read()
// does, so once every buffered event was drained, the next read silently
// parked the goroutine in Go's own runtime poller waiting for that specific
// device to produce more data, which never happened - see the InputWatcher
// doc comment in input.go for the full story.
func TestInputWatcherSecondWaitTimesOutAfterDrainingEvents(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := os.Stat("/dev/input"); err != nil {
		t.Skip("no /dev/input - no evdev device nodes available in this environment")
	}

	uinputFd, err := createVirtualKeyboard(testKeyCode)
	if err != nil {
		t.Skipf("could not create virtual keyboard via uinput (is the module loaded? try: modprobe uinput): %v", err)
	}
	defer destroyVirtualKeyboard(uinputFd)
	time.Sleep(300 * time.Millisecond) // let udev create the corresponding /dev/input/eventN node

	watcher, err := NewInputWatcher()
	if err != nil {
		t.Fatalf("NewInputWatcher: %v", err)
	}
	defer watcher.Close()

	if err := injectKeyPress(uinputFd, testKeyCode); err != nil {
		t.Fatalf("injectKeyPress: %v", err)
	}

	result, err := watcher.WaitForKeyPress(2 * time.Second)
	if err != nil {
		t.Fatalf("first WaitForKeyPress: %v", err)
	}
	if result != WaitKeyPress {
		t.Fatalf("expected WaitKeyPress on first call, got %v", result)
	}

	// The regression case: a second wait, with no further input, must time
	// out rather than hang. Run it in a goroutine with an outer timeout so
	// a regression fails the test instead of hanging it forever.
	done := make(chan struct{})
	var secondResult WaitResult
	var secondErr error
	go func() {
		secondResult, secondErr = watcher.WaitForKeyPress(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		if secondErr != nil {
			t.Fatalf("second WaitForKeyPress: %v", secondErr)
		}
		if secondResult != WaitTimeout {
			t.Fatalf("expected WaitTimeout on second call, got %v", secondResult)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second WaitForKeyPress did not return within 5s - regression of the hang bug")
	}
}

// --- uinput helpers (see cmd/vtspike for the fully annotated original) ---

const (
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502

	evSyn     = 0x00
	synReport = 0

	busUSB            = 0x03
	uinputMaxNameSize = 80
	absCnt            = 64
)

type inputID struct {
	BusType, Vendor, Product, Version uint16
}

type uinputUserDev struct {
	Name         [uinputMaxNameSize]byte
	ID           inputID
	FFEffectsMax uint32
	AbsMax       [absCnt]int32
	AbsMin       [absCnt]int32
	AbsFuzz      [absCnt]int32
	AbsFlat      [absCnt]int32
}

type uinputInputEvent struct {
	Sec, Usec  int64
	Type, Code uint16
	Value      int32
}

func createVirtualKeyboard(key uint16) (*os.File, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	if err := ioctlInt(f, uiSetEvBit, evKey); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_EVBIT: %w", err)
	}
	if err := ioctlInt(f, uiSetKeyBit, int(key)); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_KEYBIT: %w", err)
	}

	var dev uinputUserDev
	copy(dev.Name[:], "nodematrix-daemon-test")
	dev.ID = inputID{BusType: busUSB, Vendor: 1, Product: 1, Version: 1}
	if err := binary.Write(f, binary.LittleEndian, &dev); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing uinput_user_dev: %w", err)
	}

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(uiDevCreate), 0); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", errno)
	}
	time.Sleep(200 * time.Millisecond)
	return f, nil
}

func ioctlInt(f *os.File, req uintptr, val int) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), req, uintptr(val))
	if errno != 0 {
		return errno
	}
	return nil
}

func injectKeyPress(f *os.File, key uint16) error {
	events := []uinputInputEvent{
		{Type: evKey, Code: key, Value: 1},
		{Type: evSyn, Code: synReport, Value: 0},
		{Type: evKey, Code: key, Value: 0},
		{Type: evSyn, Code: synReport, Value: 0},
	}
	for _, e := range events {
		if err := binary.Write(f, binary.LittleEndian, &e); err != nil {
			return err
		}
	}
	return nil
}

func destroyVirtualKeyboard(f *os.File) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(uiDevDestroy), 0)
	closeErr := f.Close()
	if errno != 0 {
		return errno
	}
	return closeErr
}
