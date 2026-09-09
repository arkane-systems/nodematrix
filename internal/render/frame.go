package render

import "unicode/utf8"

// framePadding is the number of blank rows/columns kept between the frame's
// border and its text content.
const framePadding = 1

const (
	defaultAlertOff = "console: quiet"
	defaultAlertOn  = "⚠ console activity while hidden"

	// minLabelValueGap is the smallest number of blank columns Layout will
	// leave between a StatusLine's label and its right-justified value.
	minLabelValueGap = 2
)

// StatusLine is one row of node status shown in the frame, e.g.
// Label "uptime", Value "4d 02:17". Render lays these out as a clean
// two-column table: label left-aligned, value right-aligned, so a set of
// status lines reads as a table rather than freeform text.
type StatusLine struct {
	Label string
	Value string
}

// Frame is the bordered status panel drawn in the center of the screen. It
// has no idea what a rain column is; Screen is responsible for making sure
// the Matrix doesn't draw into the area Frame.Rect reports.
type Frame struct {
	// Title is shown, bolded, embedded in the top border (hostname, per
	// the product requirement that the frame be "titled with a boldface
	// hostname").
	Title string
	// Lines are the body status rows (uptime, load, etc.), shown in
	// reading order.
	Lines []StatusLine
	// Alert is the "console output has occurred behind the screensaver"
	// flag; when true, the frame shows AlertOn text, styled to stand out,
	// instead of AlertOff.
	Alert bool

	X, Y, W, H int // computed by Layout; top-left corner and full size including the border
}

// NewFrame creates a Frame with the given title and no body lines yet.
func NewFrame(title string) *Frame {
	return &Frame{Title: title}
}

// Rect reports the frame's current footprint, for excluding it from the
// rain (see Matrix.Render).
func (f *Frame) Rect() Rect {
	return Rect{X: f.X, Y: f.Y, W: f.W, H: f.H}
}

// Layout sizes and centers the frame within a gridW x gridH screen, based on
// the current title and body lines. Call it again whenever Title, Lines, or
// the screen size changes.
func (f *Frame) Layout(gridW, gridH int) {
	contentWidth := utf8.RuneCountInString(f.Title) + 4 // room for the "─ title ─" top-border decoration
	for _, l := range f.Lines {
		lineWidth := utf8.RuneCountInString(l.Label) + minLabelValueGap + utf8.RuneCountInString(l.Value)
		if lineWidth > contentWidth {
			contentWidth = lineWidth
		}
	}
	if w := utf8.RuneCountInString(defaultAlertOn); w > contentWidth {
		contentWidth = w
	}

	innerWidth := contentWidth + framePadding*2
	w := innerWidth + 2 // left/right border columns
	h := len(f.Lines) + 1 /* blank spacer */ + 1 /* alert line */ + 2 /* border */ + framePadding*2

	if w > gridW {
		w = gridW
	}
	if h > gridH {
		h = gridH
	}
	if w < 4 {
		w = 4
	}
	if h < 4 {
		h = 4
	}

	f.W, f.H = w, h
	f.X = (gridW - w) / 2
	f.Y = (gridH - h) / 2
}

// Render draws the frame's border, title, status lines, and alert indicator
// into g. It always draws every cell of its own footprint, which is what
// keeps the frame's interior a clean, rain-free hole in the screen.
func (f *Frame) Render(g *Grid) {
	x0, y0, w, h := f.X, f.Y, f.W, f.H
	if w <= 0 || h <= 0 {
		return
	}

	g.Set(x0, y0, Cell{Rune: '┌', Style: StyleFrame})     // ┌
	g.Set(x0+w-1, y0, Cell{Rune: '┐', Style: StyleFrame}) // ┐
	g.Set(x0, y0+h-1, Cell{Rune: '└', Style: StyleFrame}) // └
	g.Set(x0+w-1, y0+h-1, Cell{Rune: '┘', Style: StyleFrame})

	for x := x0 + 1; x < x0+w-1; x++ {
		g.Set(x, y0, Cell{Rune: '─', Style: StyleFrame}) // ─
		g.Set(x, y0+h-1, Cell{Rune: '─', Style: StyleFrame})
	}
	for y := y0 + 1; y < y0+h-1; y++ {
		g.Set(x0, y, Cell{Rune: '│', Style: StyleFrame}) // │
		g.Set(x0+w-1, y, Cell{Rune: '│', Style: StyleFrame})
	}

	title := " " + f.Title + " "
	titleWidth := utf8.RuneCountInString(title)
	titleStart := x0 + (w-titleWidth)/2
	if titleStart < x0+1 {
		titleStart = x0 + 1
	}
	writeText(g, titleStart, y0, x0+w-1, title, StyleFrameTitle)

	textX := x0 + 1 + framePadding
	textMaxX := x0 + w - 1 - framePadding
	textY := y0 + 1 + framePadding
	for i, line := range f.Lines {
		y := textY + i
		writeText(g, textX, y, textMaxX, line.Label, StyleFrame)
		valueX := textMaxX - utf8.RuneCountInString(line.Value)
		if valueX < textX {
			valueX = textX // value too long to fully right-justify; let it start at the label column and get truncated like any other overlong text
		}
		writeText(g, valueX, y, textMaxX, line.Value, StyleFrame)
	}

	// A blank row separates the status table from the alert line below, so
	// the alert reads as a distinct, separately-flagged item rather than
	// just another row of the table.
	alertY := textY + len(f.Lines) + 1

	alertText, alertStyle := defaultAlertOff, StyleFrame
	if f.Alert {
		alertText, alertStyle = defaultAlertOn, StyleFrameAlert
	}
	writeText(g, textX, alertY, textMaxX, alertText, alertStyle)
}

// writeText draws s starting at (x, y), truncating anything at or past
// maxX (exclusive) rather than wrapping or overflowing the frame.
func writeText(g *Grid, x, y, maxX int, s string, style Style) {
	i := 0
	for _, r := range s {
		px := x + i
		if px >= maxX {
			break
		}
		g.Set(px, y, Cell{Rune: r, Style: style})
		i++
	}
}
