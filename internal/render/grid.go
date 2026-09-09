// Package render implements the visual side of nodematrix: the digital-rain
// animation and the status frame drawn on top of it. Nothing in this package
// touches a real terminal, device file, or requires any special privilege —
// it only ever reads and writes an in-memory Grid of Cells, which is what
// makes it possible to unit test the whole visual effect without a real
// console (or even a TTY at all).
package render

// Style names the small set of visual treatments a Cell can have. It maps
// onto ANSI terminal colors/attributes in ansi.go, but nothing in this file
// depends on that mapping.
type Style int

const (
	// StyleNone is the zero value: an empty, undrawn cell.
	StyleNone Style = iota
	// StyleTrail is a normal (non-leading) rain character.
	StyleTrail
	// StyleTrailBold is a normal rain character rendered bold, used to
	// approximate cmatrix's -b "bold characters" look.
	StyleTrailBold
	// StyleHead is the brightest character at the leading edge of a rain
	// column.
	StyleHead
	// StyleFrame is used for the status frame's border and body text.
	StyleFrame
	// StyleFrameTitle is used for the frame's bold hostname title.
	StyleFrameTitle
	// StyleFrameAlert is used for the "activity behind screensaver" flag
	// when it is set.
	StyleFrameAlert
)

// Cell is a single character position in the screen buffer: a rune plus how
// it should be styled. The zero Cell (Rune 0, StyleNone) means "nothing has
// been drawn here" and is treated as blank space when rendered.
type Cell struct {
	Rune  rune
	Style Style
}

// continuationRune is a sentinel (not a valid Unicode code point) marking
// the cell immediately to the right of a double-width character. Grid.ANSI
// skips it entirely rather than printing a space, since the terminal's own
// cursor has already moved past this column while drawing the wide
// character in the cell before it — see Matrix.Render.
const continuationRune rune = -1

// Grid is a fixed-size buffer of Cells addressed by (x, y), x growing right
// and y growing down, both zero-based. It has no concept of a real terminal:
// it is just a 2D array with bounds checking.
type Grid struct {
	Width  int
	Height int
	cells  []Cell
}

// NewGrid allocates a cleared Grid of the given size. Width and Height must
// both be positive.
func NewGrid(width, height int) *Grid {
	if width <= 0 || height <= 0 {
		panic("render: NewGrid requires positive width and height")
	}
	return &Grid{
		Width:  width,
		Height: height,
		cells:  make([]Cell, width*height),
	}
}

// inBounds reports whether (x, y) is a valid coordinate in the grid.
func (g *Grid) inBounds(x, y int) bool {
	return x >= 0 && x < g.Width && y >= 0 && y < g.Height
}

// At returns the cell at (x, y). Out-of-bounds coordinates return the zero
// Cell rather than panicking, since rain/frame logic finds it convenient to
// address positions near the edge without bounds-checking everywhere.
func (g *Grid) At(x, y int) Cell {
	if !g.inBounds(x, y) {
		return Cell{}
	}
	return g.cells[y*g.Width+x]
}

// Set writes a cell at (x, y). Out-of-bounds coordinates are silently
// ignored, for the same reason as At.
func (g *Grid) Set(x, y int, c Cell) {
	if !g.inBounds(x, y) {
		return
	}
	g.cells[y*g.Width+x] = c
}

// Clear resets every cell in the grid to its zero value.
func (g *Grid) Clear() {
	for i := range g.cells {
		g.cells[i] = Cell{}
	}
}

// Rect is an axis-aligned rectangle of grid cells, used to mark the status
// frame's footprint so the rain knows not to draw there.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether (x, y) falls inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Snapshot renders the grid as a plain multi-line string of the runes only
// (styles are dropped). It exists mainly for tests and for the render-demo
// tool's non-ANSI debug mode: it gives a stable, easy-to-eyeball
// representation of "what the screen currently looks like".
func (g *Grid) Snapshot() string {
	buf := make([]rune, 0, (g.Width+1)*g.Height)
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			r := g.At(x, y).Rune
			if r == 0 || r == continuationRune {
				r = ' '
			}
			buf = append(buf, r)
		}
		if y < g.Height-1 {
			buf = append(buf, '\n')
		}
	}
	return string(buf)
}
