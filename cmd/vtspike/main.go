// Command vtspike is a standalone diagnostic tool - the nodematrix plan's
// Phase 0 - that validates, on real hardware, the kernel behavior the whole
// screensaver design depends on:
//
//  1. While a different VT is the active/foreground one, a keypress does
//     NOT reach the previously-active (now inactive) VT's line discipline -
//     i.e. VT-switching genuinely isolates keyboard input, which is what
//     lets the real daemon "discard the keypress" for free rather than
//     needing to explicitly grab/suppress it.
//  2. Reading /dev/vcsN (the target VT's raw screen buffer) while that VT
//     is inactive correctly reflects new content written to it - the
//     mechanism the real daemon will use for its "console activity behind
//     the screensaver" flag.
//  3. The target VT's content survives a hide/show cycle completely intact
//     - the core "preserve what's underneath" guarantee.
//
// It must run as root (VT ioctls, /dev/uinput, and /dev/vcsN all require
// it), and needs the uinput kernel module loaded - if /dev/uinput doesn't
// exist, run `modprobe uinput` first.
//
// It's fully non-interactive: rather than asking a human to physically type
// something and eyeball the result, it creates a temporary virtual keyboard
// via /dev/uinput to inject one synthetic keypress, so the result is a
// deterministic PASS/FAIL.
//
// Usage: sudo ./vtspike [-target-vt 1]
//
// Best run over SSH rather than from the physical/virtual console itself:
// since the test's own output goes to whichever VT it was started from,
// running it from the console being tested means you won't see progress
// live - it'll all appear at once, in a rush, the moment the test switches
// that VT back to the foreground at the end.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// VT ioctl request numbers (linux/vt.h) - not provided by golang.org/x/sys/unix.
const (
	vtOpenQry    = 0x5600
	vtActivate   = 0x5606
	vtWaitActive = 0x5607
)

// uinput ioctl request numbers and struct layouts (linux/uinput.h,
// linux/input.h, linux/input-event-codes.h) - also not provided by
// golang.org/x/sys/unix. The ioctl numbers are the standard, stable
// UINPUT_IOCTL_BASE ('U') encodings.
const (
	uiSetEvBit   = 0x40045564 // _IOW('U', 100, int)
	uiSetKeyBit  = 0x40045565 // _IOW('U', 101, int)
	uiDevCreate  = 0x5501     // _IO('U', 1)
	uiDevDestroy = 0x5502     // _IO('U', 2)

	evSyn     = 0x00
	evKey     = 0x01
	synReport = 0

	busUSB = 0x03

	uinputMaxNameSize = 80
	absCnt            = 64

	defaultTestKey = 44 // KEY_Z
)

type inputID struct {
	BusType uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

// uinputUserDev mirrors struct uinput_user_dev. Field sizes here happen to
// need no padding to match the C layout (each field already starts at an
// offset satisfying the next field's alignment), so encoding/binary's tight
// packing is safe.
type uinputUserDev struct {
	Name         [uinputMaxNameSize]byte
	ID           inputID
	FFEffectsMax uint32
	AbsMax       [absCnt]int32
	AbsMin       [absCnt]int32
	AbsFuzz      [absCnt]int32
	AbsFlat      [absCnt]int32
}

// inputEvent mirrors struct input_event on amd64/arm64, where the embedded
// timeval's two fields are each a 64-bit long (giving a 24-byte struct, no
// padding needed for the same reason as above).
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

func main() {
	targetVT := flag.Int("target-vt", 1, "the VT to validate (normally tty1, where login runs)")
	testKey := flag.Uint("test-key", defaultTestKey, "evdev key code to synthesize for the isolation test (default: KEY_Z)")
	flag.Parse()

	if os.Geteuid() != 0 {
		fatal("must run as root (needs VT ioctls, /dev/uinput, and /dev/vcsN access)")
	}

	fmt.Printf("=== nodematrix Phase 0 spike: validating VT-switch isolation and vcs preservation for VT %d ===\n\n", *targetVT)

	vcsPath := fmt.Sprintf("/dev/vcs%d", *targetVT)

	baseline1, err := os.ReadFile(vcsPath)
	must(err, "reading baseline snapshot of "+vcsPath)
	fmt.Printf("[1/5] Captured baseline snapshot of %s (%d bytes)\n", vcsPath, len(baseline1))

	tty0, err := os.OpenFile("/dev/tty0", os.O_RDWR|unix.O_NOCTTY, 0)
	must(err, "opening /dev/tty0")
	defer tty0.Close()

	spikeVT, err := vtOpenQuery(tty0)
	must(err, "VT_OPENQRY (finding a free VT)")
	fmt.Printf("[2/5] Allocated spike VT %d; switching to it (hiding VT %d)\n", spikeVT, *targetVT)

	// Opening the VT device node is what actually allocates it in the
	// kernel; keep it open for the duration of the test.
	spikeVTPath := fmt.Sprintf("/dev/tty%d", spikeVT)
	spikeFd, err := os.OpenFile(spikeVTPath, os.O_RDWR|unix.O_NOCTTY, 0)
	must(err, "opening "+spikeVTPath)
	defer spikeFd.Close()

	must(vtActivateAndWait(tty0, spikeVT), fmt.Sprintf("switching to VT %d", spikeVT))

	fmt.Printf("[3/5] Injecting a synthetic keypress via a temporary virtual keyboard (no physical typing needed)\n")
	uinputFd, err := createVirtualKeyboard(uint16(*testKey))
	must(err, "creating virtual keyboard via /dev/uinput (is the uinput kernel module loaded? try: modprobe uinput)")
	must(injectKeyPress(uinputFd, uint16(*testKey)), "injecting synthetic keypress")
	time.Sleep(300 * time.Millisecond)
	must(destroyVirtualKeyboard(uinputFd), "destroying virtual keyboard")

	probe1, err := os.ReadFile(vcsPath)
	must(err, "reading post-keypress snapshot of "+vcsPath)

	isolationPassed := bytes.Equal(baseline1, probe1)
	printResult("Keypress isolation (inactive VT must NOT see the keystroke)", isolationPassed)
	if !isolationPassed {
		fmt.Println("    !!! VT content changed after injecting a keypress while switched away.")
		fmt.Println("    !!! This means keyboard input is NOT properly isolated to the active VT on this system.")
		showDiff(baseline1, probe1)
	}

	fmt.Printf("[4/5] Writing a marker message directly to VT %d while it's still hidden, to test vcs-diff detection\n", *targetVT)
	marker := []byte(fmt.Sprintf("NODEMATRIX-VCS-SPIKE-MARKER-%d\n", time.Now().UnixNano()))
	must(writeToVT(*targetVT, marker), fmt.Sprintf("writing marker to /dev/tty%d", *targetVT))
	time.Sleep(200 * time.Millisecond)

	probe2, err := os.ReadFile(vcsPath)
	must(err, "reading post-marker snapshot of "+vcsPath)

	detectionPassed := !bytes.Equal(probe1, probe2) && bytes.Contains(probe2, []byte("NODEMATRIX-VCS-SPIKE-MARKER"))
	printResult("vcs-diff detection (must see the marker while VT is still hidden)", detectionPassed)
	if !detectionPassed {
		fmt.Println("    !!! Did not detect the marker via /dev/vcsN while the VT was hidden.")
		fmt.Println("    !!! This means the 'console activity behind the screensaver' mechanism will not work as designed.")
	}

	fmt.Printf("[5/5] Switching back to VT %d and confirming its content survived intact\n", *targetVT)
	must(vtActivateAndWait(tty0, *targetVT), fmt.Sprintf("switching back to VT %d", *targetVT))
	time.Sleep(200 * time.Millisecond)

	probe3, err := os.ReadFile(vcsPath)
	must(err, "reading post-restore snapshot of "+vcsPath)

	preservePassed := bytes.Equal(probe2, probe3)
	printResult("Preservation across the hide/show cycle (content must be unchanged after switching back)", preservePassed)
	if !preservePassed {
		fmt.Println("    !!! VT content differs after switching back compared to just before switching back.")
		showDiff(probe2, probe3)
	}

	fmt.Println()
	if isolationPassed && detectionPassed && preservePassed {
		fmt.Println("=== ALL CHECKS PASSED - the VT-switch/evdev/vcs architecture is validated on this hardware. ===")
	} else {
		fmt.Println("=== ONE OR MORE CHECKS FAILED - see above before proceeding with the daemon implementation. ===")
		os.Exit(1)
	}
}

func vtOpenQuery(f *os.File) (int, error) {
	num := 0
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtOpenQry), uintptr(unsafe.Pointer(&num)))
	if errno != 0 {
		return 0, errno
	}
	return num, nil
}

func vtActivateAndWait(f *os.File, vt int) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtActivate), uintptr(vt)); errno != 0 {
		return errno
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtWaitActive), uintptr(vt)); errno != 0 {
		return errno
	}
	return nil
}

func writeToVT(vt int, data []byte) error {
	f, err := os.OpenFile(fmt.Sprintf("/dev/tty%d", vt), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
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
	copy(dev.Name[:], "nodematrix-vtspike")
	dev.ID = inputID{BusType: busUSB, Vendor: 1, Product: 1, Version: 1}

	if err := binary.Write(f, binary.LittleEndian, &dev); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing uinput_user_dev: %w", err)
	}

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(uiDevCreate), 0); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", errno)
	}

	// Give userspace (udev) a moment to register the new input device.
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
	events := []inputEvent{
		{Type: evKey, Code: key, Value: 1}, // press
		{Type: evSyn, Code: synReport, Value: 0},
		{Type: evKey, Code: key, Value: 0}, // release
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

func printResult(label string, passed bool) {
	status := "PASS"
	if !passed {
		status = "FAIL"
	}
	fmt.Printf("  [%s] %s\n", status, label)
}

func showDiff(a, b []byte) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	shown := 0
	for i := 0; i < n && shown < 5; i++ {
		if a[i] != b[i] {
			fmt.Printf("    byte %d: %q -> %q\n", i, a[i], b[i])
			shown++
		}
	}
	if len(a) != len(b) {
		fmt.Printf("    (also, lengths differ: %d vs %d bytes)\n", len(a), len(b))
	}
}

func must(err error, what string) {
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", what, err))
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "ERROR:", msg)
	os.Exit(1)
}
