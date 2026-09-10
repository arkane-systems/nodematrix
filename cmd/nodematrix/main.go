// Command nodematrix is the console screensaver daemon: it waits for the
// login console to go idle, then shows Matrix-style digital rain with a
// status frame on a dedicated virtual terminal, discarding the keypress
// that dismisses it. See the project README for deployment prerequisites
// (notably the console=ttyN kernel parameter).
//
// Must run as root (VT ioctls and /dev/input access both require it).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arkane-systems/nodematrix/internal/daemon"
)

func main() {
	loginVT := flag.Int("login-vt", 1, "the VT running login, to hide/reveal")
	screensaverVT := flag.Int("screensaver-vt", 63, "the VT the screensaver renders on (falls back to a dynamically-allocated VT if unavailable)")
	idleTimeout := flag.Duration("idle-timeout", 5*time.Minute, "how long the login console must be idle before the screensaver activates")
	tickRate := flag.Duration("tick-rate", 100*time.Millisecond, "animation frame interval (~10fps default; rounded to the nearest 0.1s - a termios read-timeout limitation)")
	statusInterval := flag.Duration("status-interval", 5*time.Second, "how often status content (uptime, load, etc.) is refreshed while showing")
	hostname := flag.String("hostname", defaultHostname(), "hostname shown in the frame title")
	kana := flag.Bool("enable-kana", false, "include halfwidth katakana in the rain - off by default, since not every console font has these glyphs drawn (see the README's console-font prerequisite notes); enable only once confirmed working on the target console")
	flag.Parse()

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "nodematrix must run as root (VT ioctls and /dev/input access require it)")
		os.Exit(1)
	}

	cfg := daemon.Config{
		LoginVT:               *loginVT,
		ScreensaverVT:         *screensaverVT,
		IdleTimeout:           *idleTimeout,
		TickRate:              *tickRate,
		StatusRefreshInterval: *statusInterval,
		Hostname:              *hostname,
		KanaEnabled:           *kana,
	}

	// On SIGINT/SIGTERM, daemon.Run restores the login VT before
	// returning rather than leaving the console stuck on the screensaver.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "nodematrix:", err)
		os.Exit(1)
	}
}

func defaultHostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "nodematrix"
}
