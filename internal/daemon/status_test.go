package daemon

import (
	"testing"
	"time"
)

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Second, "0m"},
		{90 * time.Second, "2m"},
		{2*time.Hour + 17*time.Minute, "2h17m"},
		{4*24*time.Hour + 2*time.Hour + 17*time.Minute, "4d 2h17m"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		b    uint64
		want string
	}{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1.0K"},
		{1932735283, "1.8G"},
		{8589934592, "8.0G"},
	}
	for _, c := range cases {
		if got := humanBytes(c.b); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.b, got, c.want)
		}
	}
}
