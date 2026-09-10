package daemon

import (
	"bytes"
	"fmt"
	"os"
)

// VCSMonitor detects whether anything has been written to a hidden VT's
// screen buffer since a baseline snapshot was taken - the mechanism behind
// the frame's "console activity behind the screensaver" flag. It reads
// /dev/vcsN, which reflects a VT's current screen contents regardless of
// whether that VT is the active/foreground one, so this works without
// interfering with (or even being visible to) whatever else is using that
// VT, such as login.
type VCSMonitor struct {
	path     string
	baseline []byte
}

// NewVCSMonitor prepares to monitor the given VT number's screen buffer.
func NewVCSMonitor(vt int) *VCSMonitor {
	return &VCSMonitor{path: fmt.Sprintf("/dev/vcs%d", vt)}
}

// Reset captures the monitored VT's current contents as the new baseline to
// diff future reads against. Call this each time the screensaver starts
// hiding that VT, so "activity" means "changed since it was last hidden".
func (m *VCSMonitor) Reset() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("reading baseline from %s: %w", m.path, err)
	}
	m.baseline = data
	return nil
}

// Changed reports whether the monitored VT's contents currently differ from
// the baseline captured by the last Reset call.
func (m *VCSMonitor) Changed() (bool, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", m.path, err)
	}
	return !bytes.Equal(data, m.baseline), nil
}
