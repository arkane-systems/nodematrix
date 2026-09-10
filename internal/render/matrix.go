package render

import "math/rand"

// column holds the animation state for one vertical strip of falling
// characters. Each column advances independently (different speeds, and
// staggered by a random delay before it first appears), which is what
// produces the asynchronous look of cmatrix's -a option rather than every
// column falling in lockstep.
type column struct {
	headRow      int    // row of the leading (brightest) character; can be negative while waiting to enter, or beyond height while falling off
	length       int    // how many rows behind the head stay lit as a fading trail
	ticksPerStep int    // number of Tick calls consumed per single row of movement (bigger = slower column)
	tickCounter  int    // counts up to ticksPerStep
	glyphChars   []rune // one entry per grid row: the character last assigned when the head passed that row
	boldMask     []bool // one entry per grid row: whether that row's character renders bold (approximating cmatrix's -b)
	wideMask     []bool // one entry per grid row: whether that row's character is double-width on screen (see Render)
}

func trailLenBounds(height int) (min, max int) {
	min = 4
	if min > height {
		min = height
	}
	max = height
	if max < min {
		max = min
	}
	return min, max
}

// respawn resets the column to fall again from above the screen after a
// random delay, with a freshly randomized speed and trail length so the
// next pass looks different from the last.
func (c *column) respawn(rng *rand.Rand, height int) {
	minLen, maxLen := trailLenBounds(height)
	c.length = minLen + rng.Intn(maxLen-minLen+1)
	c.ticksPerStep = 1 + rng.Intn(3) // 1..3: independent per-column speed
	c.headRow = -(1 + rng.Intn(height/2+1))
	c.tickCounter = 0
	for i := range c.glyphChars {
		c.glyphChars[i] = 0
		c.boldMask[i] = false
		c.wideMask[i] = false
	}
}

// newColumn creates a column already part-way through a fall, so the first
// rendered frame doesn't look artificially empty while columns "start up".
func newColumn(rng *rand.Rand, height int, kanaEnabled bool) *column {
	c := &column{
		glyphChars: make([]rune, height),
		boldMask:   make([]bool, height),
		wideMask:   make([]bool, height),
	}
	minLen, maxLen := trailLenBounds(height)
	c.length = minLen + rng.Intn(maxLen-minLen+1)
	c.ticksPerStep = 1 + rng.Intn(3)
	c.headRow = rng.Intn(height+maxLen) - maxLen

	for y := 0; y <= c.headRow && y < height; y++ {
		r, wide := randomGlyph(rng, kanaEnabled)
		c.glyphChars[y] = r
		c.boldMask[y] = rng.Intn(4) == 0
		c.wideMask[y] = wide
	}
	return c
}

// tick advances the column by one animation frame. Movement only actually
// happens once every ticksPerStep calls, which is what lets columns fall at
// different speeds while all being driven by the same overall frame rate.
func (c *column) tick(rng *rand.Rand, height int, kanaEnabled bool) {
	c.tickCounter++
	if c.tickCounter < c.ticksPerStep {
		return
	}
	c.tickCounter = 0
	c.headRow++

	if c.headRow-c.length > height {
		c.respawn(rng, height)
		return
	}

	if c.headRow >= 0 && c.headRow < height {
		r, wide := randomGlyph(rng, kanaEnabled)
		c.glyphChars[c.headRow] = r
		c.boldMask[c.headRow] = rng.Intn(4) == 0
		c.wideMask[c.headRow] = wide
	}

	// Occasionally flicker an already-lit trailing character to a new
	// glyph, matching cmatrix's habit of characters changing mid-trail.
	if c.length > 1 && rng.Intn(20) == 0 {
		row := c.headRow - (1 + rng.Intn(c.length-1))
		if row >= 0 && row < height {
			r, wide := randomGlyph(rng, kanaEnabled)
			c.glyphChars[row] = r
			c.wideMask[row] = wide
		}
	}
}

// Matrix is the digital-rain effect: a set of independently animated
// columns spanning a width x height area. It has no knowledge of frames,
// terminals, or ANSI — it only ever draws into a Grid, and never into cells
// covered by the Rect passed to Render.
type Matrix struct {
	width, height int
	columns       []*column
	rng           *rand.Rand
	kanaEnabled   bool
}

// NewMatrix creates a Matrix sized width x height. rng drives all of the
// effect's randomness (spawn timing, speed, glyph choice); pass a
// deterministically-seeded *rand.Rand for reproducible tests, or one seeded
// from real entropy for production use. A nil rng is replaced with one seeded
// from a fixed value, which is convenient for quick manual tries but should
// not be relied on for anything that wants real randomness.
//
// kanaEnabled controls whether halfwidth katakana is mixed into the rain
// (see glyphs.go) - default this to false unless the target console's font
// is confirmed to have those glyphs drawn; plenty of common console fonts,
// including Debian's default and GNU Unifont as currently packaged, don't,
// and render them as placeholder boxes instead.
func NewMatrix(width, height int, rng *rand.Rand, kanaEnabled bool) *Matrix {
	if width <= 0 || height <= 0 {
		panic("render: NewMatrix requires positive width and height")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	m := &Matrix{width: width, height: height, rng: rng, kanaEnabled: kanaEnabled}
	m.columns = make([]*column, width)
	for x := range m.columns {
		m.columns[x] = newColumn(rng, height, kanaEnabled)
	}
	return m
}

// Tick advances every column by one animation frame.
func (m *Matrix) Tick() {
	for _, c := range m.columns {
		c.tick(m.rng, m.height, m.kanaEnabled)
	}
}

// Render draws the current state of the rain into g. Any cell whose (x, y)
// falls inside exclude is left untouched — this is how the status frame
// stays a clean hole in the rain rather than getting drawn over: the caller
// clears the grid, calls Matrix.Render with the frame's footprint as
// exclude, and then draws the frame on top of the (still-blank) excluded
// area.
//
// Almost every rain glyph occupies exactly one terminal column, so one grid
// cell maps directly to one physical column. The rare emoji glyph is
// double-width, though: a terminal advances its cursor two columns for it,
// not one, so if we did nothing else about it every column after it would
// silently drift out of alignment for that row (this was the actual cause
// of the frame border misalignment seen in early testing, along with the
// full-width katakana/hiragana glyphs that used to be in the character
// pool). To keep grid index N always meaning physical column N, a wide
// glyph gets a continuation marker written into the cell immediately to its
// right, and Grid.ANSI skips emitting anything for that cell, so our
// accounting exactly matches what the terminal itself does.
func (m *Matrix) Render(g *Grid, exclude Rect) {
	type continuation struct{ x, y int }
	var continuations []continuation

	for x, c := range m.columns {
		for y := 0; y < m.height; y++ {
			if exclude.Contains(x, y) {
				continue
			}
			depth := c.headRow - y
			if depth < 0 || depth >= c.length {
				continue
			}
			r := c.glyphChars[y]
			if r == 0 {
				continue
			}
			wide := c.wideMask[y]
			if wide && (x+1 >= m.width || exclude.Contains(x+1, y)) {
				// Drawing a double-width glyph here would either run off
				// the right edge of the screen or spill its second half
				// into the frame; skip it for this tick rather than draw
				// something misleading. The column moves every tick, so
				// this is a one-frame skip, not a lasting gap.
				continue
			}
			style := StyleTrail
			switch {
			case depth == 0:
				style = StyleHead
			case c.boldMask[y]:
				style = StyleTrailBold
			}
			g.Set(x, y, Cell{Rune: r, Style: style})
			if wide {
				continuations = append(continuations, continuation{x + 1, y})
			}
		}
	}

	// Applied as a second pass, after every column has drawn its own
	// normal cells, so a wide glyph's continuation marker always wins
	// over whatever the neighboring column drew in that same cell this
	// tick (columns are independent and don't know about each other).
	for _, ct := range continuations {
		g.Set(ct.x, ct.y, Cell{Rune: continuationRune, Style: StyleNone})
	}
}
