package daemon

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// VT ioctl request numbers (linux/vt.h) - not provided by
// golang.org/x/sys/unix. These are the same values validated on real
// target hardware by cmd/vtspike.
const (
	vtOpenQryReq    = 0x5600
	vtActivateReq   = 0x5606
	vtWaitActiveReq = 0x5607
)

// vtOpenQuery asks the kernel for an unallocated VT number.
func vtOpenQuery(f *os.File) (int, error) {
	num := 0
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtOpenQryReq), uintptr(unsafe.Pointer(&num)))
	if errno != 0 {
		return 0, errno
	}
	return num, nil
}

// vtActivateAndWait switches the foreground VT to vt and blocks until the
// switch has actually completed.
func vtActivateAndWait(f *os.File, vt int) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtActivateReq), uintptr(vt)); errno != 0 {
		return errno
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(vtWaitActiveReq), uintptr(vt)); errno != 0 {
		return errno
	}
	return nil
}
