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

// Config holds the daemon's runtime settings.
type Config struct {
	LoginVT               int           // the VT running login (e.g. 1), which is hidden/revealed but never drawn on
	ScreensaverVT         int           // the VT the screensaver renders on (preferred; falls back to a dynamically-allocated one)
	IdleTimeout           time.Duration // how long the login console must be idle before the screensaver activates
	TickRate              time.Duration // animation frame interval
	StatusRefreshInterval time.Duration // how often status content (uptime, load, etc.) is recomputed while showing
	Hostname              string        // shown in the frame title
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

	vtMgr, err := NewManager(cfg.ScreensaverVT, cfg.LoginVT)
	if err != nil {
		return fmt.Errorf("setting up VT manager: %w", err)
	}
	defer vtMgr.Close()

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
	screen := render.NewScreen(width, height, cfg.Hostname, rng)
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

		// Fresh status and a cleared alert flag every time the screensaver
		// starts a new hide session - the alert auto-clears on dismiss, so
		// it should start clean here too rather than carrying over.
		screen.SetStatus(CollectStatus())
		screen.SetAlert(false)
		if err := vcsMon.Reset(); err != nil {
			log.Printf("could not baseline console-activity monitor: %v", err)
		}

		cancelled, err := animateUntilKeyPress(ctx, vtMgr, screen, vcsMon, cfg.StatusRefreshInterval)
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
func animateUntilKeyPress(ctx context.Context, vtMgr *Manager, screen *render.Screen, vcsMon *VCSMonitor, statusInterval time.Duration) (cancelled bool, err error) {
	lastStatus := time.Now()
	lastVCSCheck := time.Now()
	alertTriggered := false

	for {
		if ctx.Err() != nil {
			return true, nil
		}

		dismissed, err := vtMgr.WaitForDismiss()
		if err != nil {
			return false, fmt.Errorf("reading from screensaver VT: %w", err)
		}
		if dismissed {
			return false, nil
		}

		if ctx.Err() != nil {
			return true, nil
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

		screen.Tick()
		if _, err := vtMgr.Write([]byte(screen.Grid.ANSI())); err != nil {
			return false, fmt.Errorf("writing to screensaver VT: %w", err)
		}
	}
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
