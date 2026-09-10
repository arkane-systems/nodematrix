// Package daemon owns everything privileged and OS-specific: VT allocation
// and switching, reading raw keyboard input, and (in a later phase) reading
// /dev/vcsN and collecting system status. It is deliberately kept thin and
// mechanical - it calls into internal/render for all actual drawing, and
// has no rendering logic of its own, so that package can stay the one part
// of this project that's fully unit-testable without root or a real
// terminal.
package daemon

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Manager owns the screensaver's dedicated virtual terminal and the
// show/hide VT-switching that hides/reveals the login VT. It never draws
// on, writes to, or otherwise touches the login VT itself - hiding it is
// purely a matter of which VT is currently the foreground one; the kernel
// keeps the login VT's own screen buffer intact regardless of which VT is
// displayed, which is what makes "preserve what's underneath" free. This
// behavior was validated on real target hardware by cmd/vtspike.
type Manager struct {
	ctrl          *os.File // /dev/tty0, used for VT_ACTIVATE/VT_WAITACTIVE ioctls
	screensaverVT int
	screensaverFd *os.File
	loginVT       int
}

// NewManager opens the screensaver's VT (preferring preferredVT, falling
// back to a dynamically-allocated one via VT_OPENQRY if that number isn't
// available) and prepares to switch focus to/from loginVT.
func NewManager(preferredVT, loginVT int) (*Manager, error) {
	ctrl, err := os.OpenFile("/dev/tty0", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("opening /dev/tty0: %w", err)
	}

	vtNum, fd, err := openScreensaverVT(ctrl, preferredVT)
	if err != nil {
		ctrl.Close()
		return nil, err
	}

	return &Manager{
		ctrl:          ctrl,
		screensaverVT: vtNum,
		screensaverFd: fd,
		loginVT:       loginVT,
	}, nil
}

func openScreensaverVT(ctrl *os.File, preferredVT int) (int, *os.File, error) {
	path := fmt.Sprintf("/dev/tty%d", preferredVT)
	if fd, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY, 0); err == nil {
		return preferredVT, fd, nil
	}

	vtNum, err := vtOpenQuery(ctrl)
	if err != nil {
		return 0, nil, fmt.Errorf("preferred VT %d unavailable and VT_OPENQRY failed: %w", preferredVT, err)
	}
	fd, err := os.OpenFile(fmt.Sprintf("/dev/tty%d", vtNum), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("opening dynamically-allocated VT %d: %w", vtNum, err)
	}
	return vtNum, fd, nil
}

// Show switches the foreground VT to the screensaver's VT.
func (m *Manager) Show() error {
	return vtActivateAndWait(m.ctrl, m.screensaverVT)
}

// Hide switches the foreground VT back to the login VT.
func (m *Manager) Hide() error {
	return vtActivateAndWait(m.ctrl, m.loginVT)
}

// Write sends raw bytes (an ANSI-rendered frame) to the screensaver's VT.
func (m *Manager) Write(p []byte) (int, error) {
	return m.screensaverFd.Write(p)
}

// PrepareScreensaverTTY puts the screensaver's VT into raw mode - no
// canonical line buffering, no local echo, no signal-generating characters
// - and configures a read timeout of roughly tickRate (rounded to the
// nearest decisecond; termios read timeouts only have that granularity).
// Without this, the kernel's own tty layer echoes anything typed straight
// onto the screensaver's screen (since VT63, or whichever VT we're using,
// is a perfectly ordinary VT otherwise) - this is what's behind
// WaitForDismiss below.
func (m *Manager) PrepareScreensaverTTY(tickRate time.Duration) error {
	fd := int(m.screensaverFd.Fd())
	term, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("getting termios for screensaver VT: %w", err)
	}

	raw := *term
	raw.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	raw.Iflag &^= unix.IXON

	deciseconds := int(tickRate / (100 * time.Millisecond))
	if deciseconds < 1 {
		deciseconds = 1
	}
	if deciseconds > 255 {
		deciseconds = 255
	}
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = uint8(deciseconds)

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return fmt.Errorf("setting raw termios for screensaver VT: %w", err)
	}
	return nil
}

// WaitForDismiss blocks for roughly one tick interval (per
// PrepareScreensaverTTY) and reports whether any bytes arrived - i.e.
// whether a key was pressed. It reads directly from the screensaver's own
// VT rather than via evdev: since that VT is, by construction, the active
// one for the entire time the screensaver is showing, it is guaranteed to
// receive every keystroke through the kernel's normal keyboard dispatch,
// with no dependency on which /dev/input/eventN devices happened to exist
// when the daemon started (unlike evdev-based detection, which is used
// only for idle detection while the login VT is active and this one
// structurally can't see input at all).
//
// With VMIN=0 (set by PrepareScreensaverTTY), a read that times out with no
// data is a normal, expected outcome under termios semantics - but Go's
// os.File.Read converts that zero-byte, no-error result into io.EOF to
// satisfy the io.Reader contract, so that specific error must be treated as
// "no dismiss yet" rather than a real failure.
func (m *Manager) WaitForDismiss() (dismissed bool, err error) {
	buf := make([]byte, 32)
	n, err := m.screensaverFd.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	return n > 0, nil
}

// Size reports the screensaver VT's current character-grid dimensions, so
// the caller can size the render.Screen to match the real console - see
// the earlier discovery that consoles are not necessarily 80x25.
func (m *Manager) Size() (width, height int, err error) {
	ws, err := unix.IoctlGetWinsize(int(m.screensaverFd.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, fmt.Errorf("screensaver VT reported zero size (%dx%d)", ws.Col, ws.Row)
	}
	return int(ws.Col), int(ws.Row), nil
}

// Close releases the VT device handles. It does not switch VTs - callers
// should Hide() first if they want the login VT visible on exit.
func (m *Manager) Close() error {
	ferr := m.screensaverFd.Close()
	cerr := m.ctrl.Close()
	if ferr != nil {
		return ferr
	}
	return cerr
}
