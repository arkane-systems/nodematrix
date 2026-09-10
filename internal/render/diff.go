package render

import (
	"fmt"
	"strings"
)

// DiffRenderer serializes a Grid to ANSI incrementally rather than
// redrawing the whole screen on every call. After the first call (which is
// necessarily a full draw, since there's nothing yet to compare against),
// each subsequent call walks the grid comparing it to what was emitted last
// time and writes escape sequences only for cells that actually changed,
// using cursor-positioning to jump directly between changed runs instead of
// re-visiting every cell in between.
//
// This is the standard technique curses-style terminal UIs use to avoid
// paying for a full-screen redraw every frame, and it matters a lot here in
// practice: a rain column only actually changes the one or two rows around
// its head each time it moves (which itself only happens once every
// ticksPerStep ticks - see matrix.go), and most of a 240x67 grid is
// unrelated blank space or static frame content that's identical to the
// previous frame. Measured on real hardware, this cut steady-state CPU use
// during animation by roughly an order of magnitude compared to re-emitting
// all ~16,000 cells every tick regardless of whether they changed.
type DiffRenderer struct {
	width, height int
	prev          []Cell // nil until the first Render call, or after Reset
}

// NewDiffRenderer creates a DiffRenderer with no prior frame - its first
// Render call will be a full draw.
func NewDiffRenderer() *DiffRenderer {
	return &DiffRenderer{}
}

// Reset forgets the last-emitted state, forcing the next Render call to do
// a full redraw. Use this whenever the terminal's actual displayed content
// could have changed outside of this renderer's own Render calls.
func (d *DiffRenderer) Reset() {
	d.prev = nil
}

// Render returns the ANSI bytes needed to bring a terminal from whatever it
// displayed after the previous Render call (or an unknown state, for the
// first call or the first call after Reset, or after the grid's dimensions
// change) to match g exactly. If nothing changed since the previous call,
// it returns an empty string - there is nothing to write at all.
func (d *DiffRenderer) Render(g *Grid) string {
	if d.prev == nil || d.width != g.Width || d.height != g.Height {
		out := g.ANSI()
		d.storePrev(g)
		return out
	}

	var b strings.Builder
	cur := Style(-1) // not a valid Style, so the first written cell always emits its code
	cursorX, cursorY := -1, -1

	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			idx := y*g.Width + x
			cell := g.cells[idx]
			if cell == d.prev[idx] {
				continue
			}
			if cell.Rune == continuationRune {
				// Never targeted directly: its presence is implied by the
				// wide glyph immediately to its left being (re)drawn, which
				// advances the real cursor over this column as a side
				// effect of printing that glyph - see the cursor bookkeeping
				// below.
				continue
			}

			if x != cursorX || y != cursorY {
				fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
			}
			if cell.Style != cur {
				b.WriteString(ansiCodes[cell.Style])
				cur = cell.Style
			}
			r := cell.Rune
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)

			cursorX, cursorY = x+1, y
			if g.At(x+1, y).Rune == continuationRune {
				// This cell was wide - the real cursor just advanced two
				// columns, not one, and grid index x+1 is the continuation
				// marker we deliberately never visit above.
				cursorX++
			}
		}
	}

	d.storePrev(g)
	if b.Len() == 0 {
		return ""
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func (d *DiffRenderer) storePrev(g *Grid) {
	if cap(d.prev) < len(g.cells) {
		d.prev = make([]Cell, len(g.cells))
	}
	d.prev = d.prev[:len(g.cells)]
	copy(d.prev, g.cells)
	d.width, d.height = g.Width, g.Height
}
