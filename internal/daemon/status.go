package daemon

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arkane-systems/nodematrix/internal/render"
)

// CollectStatus gathers a fresh snapshot of node status for display in the
// frame: uptime, load average, memory and root-disk usage, and whether
// kubelet is running. Each field is collected independently and
// best-effort - a failure to read one doesn't prevent the others from
// showing, since a panel with four good lines and one "n/a" is far more
// useful than one that shows nothing because a single read failed.
//
// Hostname isn't repeated here even though it was one of the originally
// discussed fields: it's already shown, bolded, as the frame's title, so
// including it again in the body would just be redundant.
func CollectStatus() []render.StatusLine {
	lines := []render.StatusLine{
		{Label: "uptime", Value: uptimeString()},
		{Label: "load", Value: loadAverageString()},
	}
	if used, total, ok := memoryUsage(); ok {
		lines = append(lines, render.StatusLine{Label: "mem", Value: fmt.Sprintf("%s / %s", humanBytes(used), humanBytes(total))})
	} else {
		lines = append(lines, render.StatusLine{Label: "mem", Value: "n/a"})
	}
	if used, total, ok := diskUsage("/"); ok {
		lines = append(lines, render.StatusLine{Label: "disk", Value: fmt.Sprintf("%s / %s", humanBytes(used), humanBytes(total))})
	} else {
		lines = append(lines, render.StatusLine{Label: "disk", Value: "n/a"})
	}
	lines = append(lines, render.StatusLine{Label: "kubelet", Value: serviceStatus("kubelet")})
	return lines
}

func uptimeString() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "n/a"
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "n/a"
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "n/a"
	}
	return formatUptime(time.Duration(seconds * float64(time.Second)))
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh%02dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func loadAverageString() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "n/a"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "n/a"
	}
	return strings.Join(fields[:3], " ")
}

func memoryUsage() (used, total uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	var memTotalKB, memAvailableKB uint64
	haveTotal, haveAvailable := false, false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			memTotalKB, haveTotal = parseMeminfoLine(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			memAvailableKB, haveAvailable = parseMeminfoLine(line)
		}
		if haveTotal && haveAvailable {
			break
		}
	}
	if !haveTotal || !haveAvailable {
		return 0, 0, false
	}
	total = memTotalKB * 1024
	available := memAvailableKB * 1024
	if available > total {
		available = total
	}
	return total - available, total, true
}

func parseMeminfoLine(line string) (uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func diskUsage(path string) (used, total uint64, ok bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if free > total {
		free = total
	}
	return total - free, total, true
}

// humanBytes formats a byte count as a short binary-prefixed string, e.g.
// "1.8G" or "100.0G".
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

// serviceStatus runs `systemctl is-active <name>` and reports its output
// ("active", "inactive", "failed", etc.) - a best-effort way to tell whether
// a unit like kubelet is up, without needing to talk to systemd's D-Bus API
// directly for one simple check. systemctl exits non-zero for anything but
// "active", but still prints a meaningful state to stdout in that case, so
// only a genuinely empty result (e.g. systemctl itself missing) falls back
// to "unknown".
func serviceStatus(name string) string {
	out, _ := exec.Command("systemctl", "is-active", name).Output()
	status := strings.TrimSpace(string(out))
	if status == "" {
		return "unknown"
	}
	return status
}
