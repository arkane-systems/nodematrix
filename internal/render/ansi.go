package render

import "strings"

// ansiCodes maps each Style to the ANSI/VT100 SGR (Select Graphic Rendition)
// escape sequence that produces it. These are basic codes understood by the
// Linux kernel's own virtual console emulation, so no terminfo lookups are
// needed to use them on the actual target device.
var ansiCodes = map[Style]string{
	StyleNone:       "\x1b[0m",
	StyleTrail:      "\x1b[0;32m", // normal green
	StyleTrailBold:  "\x1b[1;32m", // bold green
	StyleHead:       "\x1b[1;37m", // bold white: the brightest, leading character
	StyleFrame:      "\x1b[0m",    // terminal default, so the frame reads as distinct from the green rain
	StyleFrameTitle: "\x1b[1m",    // bold, per "titled with a boldface hostname"
	StyleFrameAlert: "\x1b[1;31m", // bold red
}

// ANSI renders the whole grid as one string of ANSI escapes and text,
// suitable for writing directly to a console device to redraw the screen in
// place. It homes the cursor first rather than relying on scrolling, so
// writing it repeatedly on a timer redraws the same screen without drift.
// Adjacent cells that share a Style reuse the same escape code instead of
// re-emitting it per cell, which keeps repeated full-screen redraws cheap.
func (g *Grid) ANSI() string {
	var b strings.Builder
	b.WriteString("\x1b[H")

	cur := Style(-1) // not a valid Style, so the first cell always emits its code
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			c := g.At(x, y)
			if c.Rune == continuationRune {
				// The cell before this one was double-width and already
				// advanced the terminal's cursor past this column; emit
				// nothing here at all, not even a space.
				continue
			}
			if c.Style != cur {
				b.WriteString(ansiCodes[c.Style])
				cur = c.Style
			}
			r := c.Rune
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		if y < g.Height-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
