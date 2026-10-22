package render

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// vt is a minimal terminal emulator: just enough to replay the ANSI bytes
// DiffRenderer/Grid.ANSI actually produce (absolute cursor positioning,
// SGR codes, and \r\n line endings as emitted by Grid.ANSI's full-draw
// path) and recover what a real terminal would be displaying afterward.
// This exists purely to answer one question: does the byte stream
// nodematrix writes, replayed faithfully, ever diverge from what the Grid
// it was generated from actually contains? If DiffRenderer's cursor
// bookkeeping or cell-equality check has a bug that causes it to under-emit
// (skip a cell that actually needs redrawing) or misposition the cursor,
// this is how it would show up - as a point where vt's reconstructed
// screen stops matching Grid.Snapshot(), with nothing external involved at
// all.
type vt struct {
	width, height int
	cells         [][]rune
	cx, cy        int // 0-indexed
}

func newVT(width, height int) *vt {
	cells := make([][]rune, height)
	for y := range cells {
		row := make([]rune, width)
		for x := range row {
			row[x] = ' '
		}
		cells[y] = row
	}
	return &vt{width: width, height: height, cells: cells}
}

// feed processes one Render()/ANSI() output string, updating the emulated
// screen exactly as a real terminal would.
func (v *vt) feed(s string) {
	i := 0
	for i < len(s) {
		switch s[i] {
		case '\x1b':
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
			}
			start := j
			for j < len(s) && !isFinalByte(s[j]) {
				j++
			}
			if j >= len(s) {
				i = j
				break
			}
			params := s[start:j]
			final := s[j]
			switch final {
			case 'H':
				row, col := 1, 1
				fmt.Sscanf(params, "%d;%d", &row, &col)
				if params == "" {
					row, col = 1, 1
				}
				v.cy, v.cx = row-1, col-1
			case 'm':
				// SGR: no effect on cursor or content.
			}
			i = j + 1
		case '\r':
			v.cx = 0
			i++
		case '\n':
			v.cy++
			v.cx = 0
			i++
		default:
			r, size := decodeRune(s[i:])
			if v.cy >= 0 && v.cy < v.height && v.cx >= 0 && v.cx < v.width {
				v.cells[v.cy][v.cx] = r
			}
			v.cx++
			i += size
		}
	}
}

func isFinalByte(b byte) bool {
	return b == 'H' || b == 'm'
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, len(string(r))
	}
	return ' ', 1
}

func (v *vt) snapshot() string {
	var b strings.Builder
	for y, row := range v.cells {
		b.WriteString(string(row))
		if y < len(v.cells)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestDiffRendererReplayMatchesGridAcrossManyTicks drives a full Screen
// (realistic production size, kana disabled - matching the actual
// deployment this was investigated against) through many ticks, mixed with
// periodic SetStatus content-length changes and Resize calls, replaying
// every ANSI() output onto a vt and checking it matches Grid.Snapshot()
// after every single tick. Any divergence is a real bug in the
// diff/cursor/resize bookkeeping, independent of anything external.
func TestDiffRendererReplayMatchesGridAcrossManyTicks(t *testing.T) {
	const width, height = 240, 67
	rng := rand.New(rand.NewSource(42))

	s := NewScreen(width, height, "princess-celestia", rng, false)
	term := newVT(width, height)

	statusVariants := [][]StatusLine{
		{{Label: "uptime", Value: "4d 02:17"}, {Label: "load", Value: "1.59 1.42 1.43"}, {Label: "mem", Value: "13.4G / 62.5G"}, {Label: "disk", Value: "269.9G / 8454.0G"}, {Label: "kubelet", Value: "active"}},
		{{Label: "uptime", Value: "41d 23h59m"}, {Label: "load", Value: "0.00 0.01 0.05"}, {Label: "mem", Value: "1.8G / 62.5G"}, {Label: "disk", Value: "100.0G / 8454.0G"}, {Label: "kubelet", Value: "unknown"}},
		{{Label: "uptime", Value: "9m"}, {Label: "load", Value: "12.59 11.42 10.43"}, {Label: "mem", Value: "62.5G / 62.5G"}, {Label: "disk", Value: "8454.0G / 8454.0G"}, {Label: "kubelet", Value: "failed"}},
	}

	const totalTicks = 20000
	for i := 0; i < totalTicks; i++ {
		if i%137 == 0 {
			s.SetStatus(statusVariants[(i/137)%len(statusVariants)])
		}
		if i%971 == 0 {
			s.SetAlert((i/971)%2 == 0)
		}
		if i%4001 == 0 && i > 0 {
			// Simulate a resize - e.g. a transient HDMI link drop that
			// settles back to the same or a different size.
			nw, nh := width, height
			if (i/4001)%3 == 1 {
				nw, nh = width-7, height-3
			}
			s.Resize(nw, nh, rng)
			term = newVT(nw, nh)
		}
		if i%6007 == 0 && i > 0 {
			s.ForceFullRedraw()
		}

		s.Tick()
		out := s.ANSI()
		term.feed(out)

		want := s.Grid.Snapshot()
		got := term.snapshot()
		if got != want {
			t.Fatalf("tick %d: replayed terminal diverged from Grid\n--- got ---\n%s\n--- want ---\n%s", i, got, want)
		}
	}
}
