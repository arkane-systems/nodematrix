package daemon

import (
	"strings"
	"testing"
)

func TestCheckConsolesOutputWarnsOnTTY0(t *testing.T) {
	const proc = "tty0                 -WU (ECp)       4:1\n"
	if warning := checkConsolesOutput(strings.NewReader(proc)); warning == "" {
		t.Error("expected a warning when /proc/consoles shows tty0")
	}
}

func TestCheckConsolesOutputQuietOnPinnedVT(t *testing.T) {
	const proc = "tty1                 -WU (ECp)       4:1\n"
	if warning := checkConsolesOutput(strings.NewReader(proc)); warning != "" {
		t.Errorf("expected no warning when console is pinned to tty1, got: %s", warning)
	}
}

func TestCheckConsolesOutputQuietOnEmpty(t *testing.T) {
	if warning := checkConsolesOutput(strings.NewReader("")); warning != "" {
		t.Errorf("expected no warning for empty input, got: %s", warning)
	}
}
