package daemon

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arkane-systems/nodematrix/internal/render"
)

// lockPath is a single-instance guard: two nodematrix processes fighting
// over the same VT devices would produce exactly the kind of confusing,
// inconsistent behavior (stray keypresses, one instance's Ctrl+C not
// reaching the instance actually driving the console) that's easy to
// mistake for a logic bug. /run is always a writable tmpfs for root and is
// cleared across reboots, which is the right lifetime for a lock like this.
const lockPath = "/run/nodematrix.lock"

// vcsCheckInterval is how often the screensaver checks whether anything has
// been written to the hidden login VT while it's covering it. Console
// output doesn't happen fast, so there's no benefit to checking anywhere
// near the animation tick rate.
const vcsCheckInterval = time.Second

// fullRedrawInterval is how often, during a show session, the screensaver
// forces a full redraw instead of relying on the diff renderer's cache - see
// Screen.ForceFullRedraw. This is a self-healing safety net against
// corruption introduced by anything else writing to the screensaver VT
// outside nodematrix's knowledge, not a performance-critical cadence, so it
// doesn't need to be configurable: amortized over fullRedrawInterval, a full
// redraw's cost is negligible next to the steady-state saving diff
// rendering already provides.
const fullRedrawInterval = 30 * time.Second

// Config holds the daemon's runtime settings.
type Config struct {
	LoginVT               int           // the VT running login (e.g. 1), which is hidden/revealed but never drawn on
	ScreensaverVT         int           // the VT the screensaver renders on (preferred; falls back to a dynamically-allocated one)
	IdleTimeout           time.Duration // how long the login console must be idle before the screensaver activates
	TickRate              time.Duration // animation frame interval
	StatusRefreshInterval time.Duration // how often status content (uptime, load, etc.) is recomputed while showing
	Hostname              string        // shown in the frame title
	KanaEnabled           bool          // include halfwidth katakana in the rain; off by default, see render.NewMatrix
}

// Run drives the idle -> show -> (keypress) -> hide state machine until ctx
// is cancelled (e.g. by a SIGINT/SIGTERM handler in main), at which point
// it restores the login VT before returning - a daemon that manipulates the
// shared console must not exit leaving it stuck on the screensaver VT.
func Run(ctx context.Context, cfg Config) error {
	lock, err := acquireSingleInstanceLock(lockPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	log.Printf("starting: login-vt=%d screensaver-vt=%d idle-timeout=%s tick-rate=%s hostname=%q",
		cfg.LoginVT, cfg.ScreensaverVT, cfg.IdleTimeout, cfg.TickRate, cfg.Hostname)

	if warning := checkConsoleConfig(); warning != "" {
		log.Printf("WARNING: %s", warning)
	}

	vtMgr, err := NewManager(cfg.ScreensaverVT, cfg.LoginVT)
	if err != nil {
		return fmt.Errorf("setting up VT manager: %w", err)
	}
	defer vtMgr.Close()

	if vtMgr.VT() != cfg.ScreensaverVT {
		log.Printf("preferred screensaver VT %d was unavailable; using dynamically-allocated VT %d instead - "+
			"check whether a getty or other process is also attached to it, since nodematrix does not have it exclusively",
			cfg.ScreensaverVT, vtMgr.VT())
	}
	log.Printf("screensaver VT in use: %d", vtMgr.VT())

	if err := vtMgr.PrepareScreensaverTTY(cfg.TickRate); err != nil {
		return fmt.Errorf("preparing screensaver VT: %w", err)
	}

	watcher, err := NewInputWatcher()
	if err != nil {
		return fmt.Errorf("setting up input watcher: %w", err)
	}
	defer watcher.Close()

	// Translate ctx cancellation into a watcher wake-up during the idle
	// wait below, since WaitForKeyPress otherwise has no way to know
	// about it while blocked in epoll_wait for as long as IdleTimeout.
	go func() {
		<-ctx.Done()
		log.Printf("shutdown requested")
		watcher.Cancel()
	}()

	width, height, err := vtMgr.Size()
	if err != nil {
		return fmt.Errorf("querying screensaver VT size: %w", err)
	}
	log.Printf("screensaver VT ready: %dx%d", width, height)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	screen := render.NewScreen(width, height, cfg.Hostname, rng, cfg.KanaEnabled)
	vcsMon := NewVCSMonitor(cfg.LoginVT)

	for {
		log.Printf("waiting for idle (timeout=%s)...", cfg.IdleTimeout)
		result, err := watcher.WaitForKeyPress(cfg.IdleTimeout)
		if err != nil {
			return fmt.Errorf("waiting for input: %w", err)
		}
		switch result {
		case WaitCancelled:
			log.Printf("cancelled while idle-waiting; exiting (login VT was never hidden)")
			return nil
		case WaitKeyPress:
			log.Printf("activity detected via evdev; resetting idle timer")
			continue
		}

		log.Printf("idle timeout reached; showing screensaver")
		if err := vtMgr.Show(); err != nil {
			return fmt.Errorf("activating screensaver VT: %w", err)
		}

		// Re-check the VT's size on every show rather than trusting the one
		// captured at startup - see checkAndHandleResize.
		width, height = checkAndHandleResize(vtMgr, screen, rng, width, height)

		// Fresh status and a cleared alert flag every time the screensaver
		// starts a new hide session - the alert auto-clears on dismiss, so
		// it should start clean here too rather than carrying over.
		screen.SetStatus(CollectStatus())
		screen.SetAlert(false)
		screen.ForceFullRedraw() // clear out any corruption accumulated on the VT while hidden
		if err := vcsMon.Reset(); err != nil {
			log.Printf("could not baseline console-activity monitor: %v", err)
		}

		var cancelled bool
		cancelled, err, width, height = animateUntilKeyPress(ctx, vtMgr, screen, vcsMon, cfg.StatusRefreshInterval, rng, width, height)
		if err != nil {
			vtMgr.Hide() // best-effort: don't leave the console stuck on the screensaver VT
			return err
		}
		if cancelled {
			log.Printf("cancelled while showing; restoring login VT and exiting")
		} else {
			log.Printf("dismissed by keypress; restoring login VT")
		}

		if err := vtMgr.Hide(); err != nil {
			return fmt.Errorf("restoring login VT: %w", err)
		}
		if cancelled {
			return nil
		}
	}
}

// animateUntilKeyPress redraws the screen on every tick until a keypress
// dismisses it or ctx is cancelled. It reads the dismiss keypress directly
// from the screensaver's own VT (see Manager.WaitForDismiss) rather than
// via evdev, and checks ctx between reads - each read is bounded to about
// one tick interval by PrepareScreensaverTTY, so cancellation is noticed
// within roughly one animation frame.
//
// Status content and the console-activity check are both refreshed on
// their own, much slower cadences than the animation tick, driven by wall
// clock time rather than a tick count so their actual cadence doesn't drift
// if the tick rate is reconfigured.
//
// width and height are the screensaver VT's size as of the last check
// (typically what Run captured at the start of this show session); they,
// and screen, are both re-checked periodically during the session - not
// just at its start - since a transient resize (e.g. a flaky HDMI link
// briefly dropping and renegotiating) can happen at any point while
// showing, not only in the moment between sessions. The returned width and
// height let the caller keep its own copies in sync, so the next session
// doesn't redundantly detect and rebuild for a change this one already
// handled.
func animateUntilKeyPress(ctx context.Context, vtMgr *Manager, screen *render.Screen, vcsMon *VCSMonitor, statusInterval time.Duration, rng *rand.Rand, width, height int) (cancelled bool, err error, newWidth, newHeight int) {
	lastStatus := time.Now()
	lastVCSCheck := time.Now()
	lastFullRedraw := time.Now()
	alertTriggered := false

	for {
		if ctx.Err() != nil {
			return true, nil, width, height
		}

		dismissed, err := vtMgr.WaitForDismiss()
		if err != nil {
			return false, fmt.Errorf("reading from screensaver VT: %w", err), width, height
		}
		if dismissed {
			return false, nil, width, height
		}

		if ctx.Err() != nil {
			return true, nil, width, height
		}

		if time.Since(lastStatus) >= statusInterval {
			screen.SetStatus(CollectStatus())
			lastStatus = time.Now()
		}

		if !alertTriggered && time.Since(lastVCSCheck) >= vcsCheckInterval {
			if changed, err := vcsMon.Changed(); err == nil && changed {
				alertTriggered = true
				screen.SetAlert(true)
			}
			lastVCSCheck = time.Now()
		}

		if time.Since(lastFullRedraw) >= fullRedrawInterval {
			// Covers two distinct failure modes with one check: a same-size
			// rewrite by something else with access to this VT (not caught
			// by checkAndHandleResize at all, since the dimensions never
			// changed) and a transient resize that already reverted by the
			// time this runs (caught because Resize's own full redraw
			// subsumes ForceFullRedraw - calling both anyway is harmless).
			width, height = checkAndHandleResize(vtMgr, screen, rng, width, height)
			screen.ForceFullRedraw()
			lastFullRedraw = time.Now()
		}

		screen.Tick()
		// screen.ANSI() only returns bytes for cells that actually changed
		// since the last call (see render.DiffRenderer) - often nothing,
		// for large stretches of a typical grid, so skip the write
		// entirely rather than issuing a zero-byte syscall for no reason.
		if out := screen.ANSI(); out != "" {
			if _, err := vtMgr.Write([]byte(out)); err != nil {
				return false, fmt.Errorf("writing to screensaver VT: %w", err), width, height
			}
		}
	}
}

// checkAndHandleResize re-queries the screensaver VT's actual size and, if
// it no longer matches width/height, rebuilds screen for the new size via
// Screen.Resize and logs what changed. It returns the (possibly updated)
// width and height so the caller's own copies stay in sync. nodematrix is
// never the screensaver VT's foreground process group (it's a bare systemd
// service with no controlling tty), so it never receives a SIGWINCH if that
// VT's real character geometry changes - whether from something settling
// late during boot or from a transient display-link drop (e.g. a flaky
// HDMI switch toggling hot-plug-detect) - which is why this has to poll
// instead of just reacting to one.
func checkAndHandleResize(vtMgr *Manager, screen *render.Screen, rng *rand.Rand, width, height int) (int, int) {
	w, h, err := vtMgr.Size()
	if err != nil {
		log.Printf("could not re-check screensaver VT size: %v", err)
		return width, height
	}
	if w == width && h == height {
		return width, height
	}
	log.Printf("screensaver VT size changed: %dx%d -> %dx%d; rebuilding screen", width, height, w, h)
	screen.Resize(w, h, rng)
	return w, h
}

// acquireSingleInstanceLock takes an exclusive, non-blocking flock on path,
// failing fast with a clear error if another nodematrix process already
// holds it rather than letting two instances silently fight over the same
// VT devices. The lock is released automatically when the process exits for
// any reason, including a killed (even SIGKILL'd) process, since the kernel
// releases flocks when the owning file descriptor is closed - so a crashed
// or force-killed prior instance never leaves a stale lock behind.
func acquireSingleInstanceLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another nodematrix instance is already running (failed to lock %s): %w", path, err)
	}
	return f, nil
}
