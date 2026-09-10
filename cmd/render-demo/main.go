// Command render-demo drives the internal/render package and prints it to
// the current terminal on a timer, so the digital-rain effect and status
// frame can be watched and iterated on visually without any of the
// privileged VT/console-device code from internal/daemon — this is exactly
// the "implement and test the display separately" split the project wants.
//
// Run it in any normal terminal (or a tmux pane) with:
//
//	go run ./cmd/render-demo
//
// Press Ctrl+C to quit. Status values refresh every statusRefreshInterval
// (simulated here; the real daemon will do the same with live system
// stats), and the alert flag auto-toggles on the same schedule so both its
// states are visible without needing to restart the demo.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arkane-systems/nodematrix/internal/render"
)

// statusRefreshInterval is how often status content (uptime, load, etc.) is
// refreshed - deliberately much slower than the animation tick rate, since
// this kind of system status doesn't change fast enough to be worth
// recomputing on every frame.
const statusRefreshInterval = 5 * time.Second

func main() {
	width := flag.Int("width", 0, "screen width in columns (0 = auto-detect from the terminal)")
	height := flag.Int("height", 0, "screen height in rows (0 = auto-detect from the terminal)")
	hostname := flag.String("hostname", demoHostname(), "hostname shown in the frame title")
	alert := flag.Bool("alert", false, "start with the console-activity alert flag set (it auto-toggles every 5s regardless, so both styles are visible)")
	fps := flag.Float64("fps", 12, "animation frames per second")
	kana := flag.Bool("kana", false, "include halfwidth katakana in the rain (off by default - not every console font has these glyphs drawn)")
	flag.Parse()

	w, h := resolveSize(*width, *height)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	screen := render.NewScreen(w, h, *hostname, rng, *kana)

	startedAt := time.Now()
	screen.SetStatus(demoStatusLines(startedAt))
	alertOn := *alert
	screen.SetAlert(alertOn)

	// Hide the cursor for the duration of the demo and always restore it
	// (and the terminal's scrollback) on exit, including on Ctrl+C.
	fmt.Print("\x1b[?25l\x1b[2J")
	defer fmt.Print("\x1b[?25h")

	animTick := time.NewTicker(time.Duration(float64(time.Second) / *fps))
	defer animTick.Stop()
	statusTick := time.NewTicker(statusRefreshInterval)
	defer statusTick.Stop()

	for {
		select {
		case <-animTick.C:
			screen.Tick()
			fmt.Print(screen.ANSI())
		case <-statusTick.C:
			screen.SetStatus(demoStatusLines(startedAt))
			alertOn = !alertOn
			screen.SetAlert(alertOn)
		}
	}
}

// resolveSize picks the screen size to use: an explicitly-given flag value
// wins for that dimension, otherwise it's auto-detected from the real
// terminal window (via the TIOCGWINSZ ioctl, the standard way a Unix
// program asks its controlling terminal how big it is), falling back to a
// conservative default if detection isn't possible at all - e.g. because
// stdout has been redirected to a file rather than a real terminal.
func resolveSize(flagWidth, flagHeight int) (width, height int) {
	width, height = flagWidth, flagHeight
	if width > 0 && height > 0 {
		return width, height
	}
	if dw, dh, ok := terminalSize(int(os.Stdout.Fd())); ok {
		if width <= 0 {
			width = dw
		}
		if height <= 0 {
			height = dh
		}
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func terminalSize(fd int) (width, height int, ok bool) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}

func demoHostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "nodematrix-demo"
}

// demoStatusLines fabricates status content that changes over time (a real
// uptime counter, jittering load figures), purely so this demo can show
// that Screen.SetStatus genuinely refreshes the frame - the real daemon
// will call SetStatus the same way, with real system stats in place of
// this.
func demoStatusLines(startedAt time.Time) []render.StatusLine {
	uptime := time.Since(startedAt).Round(time.Second)
	load1 := 0.20 + rand.Float64()*0.30
	return []render.StatusLine{
		{Label: "uptime", Value: uptime.String()},
		{Label: "load", Value: fmt.Sprintf("%.2f %.2f %.2f", load1, load1*0.9, load1*0.8)},
		{Label: "mem", Value: "1.8G / 8.0G"},
		{Label: "disk", Value: "12G / 100G"},
		{Label: "kubelet", Value: "active"},
	}
}
