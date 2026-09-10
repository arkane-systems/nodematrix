package daemon

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// checkConsoleConfig reads /proc/consoles and reports a warning message
// (empty if everything looks fine) if kernel/systemd console output is not
// pinned to a specific VT. Without an explicit console=ttyN kernel
// parameter, Linux routes console output through "tty0" - a dynamic alias
// for "whichever VT is currently active" - which corrupts the screensaver's
// display and loses the "preserve what's underneath" guarantee the moment
// nodematrix switches the foreground VT away: see the project's
// console=tty1 deployment prerequisite (applied via the set-console-tty1
// Ansible playbook) and the SOFA TIL this project published about it.
//
// This is advisory only - nodematrix still runs regardless of what it
// finds, since refusing to start over a misconfigured console would be
// more disruptive than the corruption it's warning about, and the operator
// may have a reason not to have applied the prerequisite yet (e.g. testing
// before a reboot window).
func checkConsoleConfig() string {
	f, err := os.Open("/proc/consoles")
	if err != nil {
		return "" // can't check; don't warn about something we couldn't verify
	}
	defer f.Close()
	return checkConsolesOutput(f)
}

// checkConsolesOutput parses /proc/consoles-formatted content (factored out
// from checkConsoleConfig for testing without needing a real /proc).
func checkConsolesOutput(r io.Reader) string {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "tty0" {
			return "console output appears to follow the active VT (tty0 in /proc/consoles) rather than a fixed VT - " +
				"kernel/systemd messages may be lost or corrupt the screensaver display once it's shown. " +
				"Add an explicit console=ttyN kernel parameter (e.g. console=tty1) and reboot before relying on this."
		}
	}
	return ""
}
