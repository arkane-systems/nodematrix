package render

import (
	"math/rand"
	"strings"
	"testing"
)

func TestGridSetAtBounds(t *testing.T) {
	g := NewGrid(5, 3)
	g.Set(2, 1, Cell{Rune: 'x', Style: StyleTrail})
	if c := g.At(2, 1); c.Rune != 'x' {
		t.Fatalf("expected 'x' at (2,1), got %q", c.Rune)
	}

	// Out-of-bounds writes/reads must not panic, and must read back as
	// the zero Cell.
	g.Set(-1, 0, Cell{Rune: 'z'})
	g.Set(100, 100, Cell{Rune: 'z'})
	if c := g.At(-1, 0); c != (Cell{}) {
		t.Errorf("expected zero cell for out-of-bounds read, got %+v", c)
	}
}

func TestGridClear(t *testing.T) {
	g := NewGrid(4, 4)
	g.Set(0, 0, Cell{Rune: 'a'})
	g.Clear()
	if c := g.At(0, 0); c != (Cell{}) {
		t.Errorf("expected cleared cell, got %+v", c)
	}
}

// TestMatrixNeverDrawsInsideExcludedRect is the key invariant behind the
// product requirement that the rain must serve strictly as a background and
// never display inside, or otherwise disturb, the status frame.
func TestMatrixNeverDrawsInsideExcludedRect(t *testing.T) {
	const width, height = 40, 20
	rng := rand.New(rand.NewSource(1))
	m := NewMatrix(width, height, rng)
	g := NewGrid(width, height)
	exclude := Rect{X: 10, Y: 5, W: 20, H: 10}

	for tick := 0; tick < 200; tick++ {
		m.Tick()
		g.Clear()
		m.Render(g, exclude)

		for y := exclude.Y; y < exclude.Y+exclude.H; y++ {
			for x := exclude.X; x < exclude.X+exclude.W; x++ {
				if c := g.At(x, y); c != (Cell{}) {
					t.Fatalf("tick %d: rain drew into excluded rect at (%d,%d): %+v", tick, x, y, c)
				}
			}
		}
	}
}

func TestMatrixDeterministicWithSameSeed(t *testing.T) {
	const width, height = 30, 15

	run := func(seed int64) string {
		rng := rand.New(rand.NewSource(seed))
		m := NewMatrix(width, height, rng)
		g := NewGrid(width, height)
		for i := 0; i < 50; i++ {
			m.Tick()
		}
		m.Render(g, Rect{})
		return g.Snapshot()
	}

	a, b := run(42), run(42)
	if a != b {
		t.Error("expected identical rain output for identical seed, got different output")
	}

	c := run(43)
	if a == c {
		t.Error("expected different rain output for a different seed")
	}
}

func TestGlyphPoolsCoverExpectedRanges(t *testing.T) {
	if len(kanaGlyphs) == 0 {
		t.Fatal("kana glyph pool is empty")
	}
	for _, r := range kanaGlyphs {
		if r < 0xFF66 || r > 0xFF9F {
			t.Fatalf("unexpected rune %U in kana pool: expected only halfwidth katakana (U+FF66-FF9F), which is guaranteed single-width", r)
		}
	}

	// Emoji are currently disabled (weightEmoji == 0, see glyphs.go), so
	// randomGlyph should never produce one in normal use.
	rng := rand.New(rand.NewSource(7))
	seenKana := false
	for i := 0; i < 5000; i++ {
		r, wide := randomGlyph(rng)
		switch {
		case containsRune(kanaGlyphs, r):
			seenKana = true
			if wide {
				t.Fatalf("kana glyph %U reported as wide; halfwidth katakana must always be single-width", r)
			}
		case containsRune(emojiGlyphs, r):
			t.Fatalf("emoji glyph %U produced despite weightEmoji being 0", r)
		default:
			if wide {
				t.Fatalf("ascii glyph %U unexpectedly reported as wide", r)
			}
		}
	}
	if !seenKana {
		t.Error("expected at least one kana glyph over 5000 samples")
	}
}

// The following tests exercise the double-width ("wide glyph") handling in
// Matrix.Render directly, by forcing a column into a wide state, rather than
// relying on randomGlyph to naturally produce one. That's necessary because
// emoji - the only wide glyphs currently in the pool - are disabled in
// normal use (see weightEmoji in glyphs.go); the intent is to keep this
// machinery working and verified for whenever a wide glyph is enabled
// again, rather than let it silently rot.

// forceWideGlyph reaches into column x's state to make its head, at the
// given row, a specific wide glyph - bypassing randomGlyph and its current
// weighting entirely.
func forceWideGlyph(m *Matrix, x, row int, r rune) {
	c := m.columns[x]
	c.headRow = row
	c.length = 3
	c.glyphChars[row] = r
	c.wideMask[row] = true
}

func TestWideGlyphsGetContinuationMarker(t *testing.T) {
	const width, height = 10, 5
	m := NewMatrix(width, height, rand.New(rand.NewSource(1)))
	g := NewGrid(width, height)

	forceWideGlyph(m, 3, 2, emojiGlyphs[0])
	m.Render(g, Rect{})

	if got := g.At(3, 2); got.Rune != emojiGlyphs[0] {
		t.Fatalf("expected forced wide glyph at (3,2), got %+v", got)
	}
	if got := g.At(4, 2); got.Rune != continuationRune {
		t.Fatalf("expected continuation marker at (4,2), got %+v", got)
	}
}

func TestWideGlyphAtRightEdgeIsSkipped(t *testing.T) {
	const width, height = 10, 5
	m := NewMatrix(width, height, rand.New(rand.NewSource(1)))
	g := NewGrid(width, height)

	forceWideGlyph(m, width-1, 2, emojiGlyphs[0]) // rightmost column: no room for a continuation cell
	m.Render(g, Rect{})

	if got := g.At(width-1, 2); got.Rune != 0 {
		t.Fatalf("expected wide glyph at the right screen edge to be skipped entirely, got %+v", got)
	}
}

func TestWideGlyphAdjacentToFrameIsSkipped(t *testing.T) {
	const width, height = 10, 5
	m := NewMatrix(width, height, rand.New(rand.NewSource(1)))
	g := NewGrid(width, height)
	exclude := Rect{X: 4, Y: 0, W: 3, H: height}

	forceWideGlyph(m, 3, 2, emojiGlyphs[0]) // immediately left of the frame
	m.Render(g, exclude)

	if got := g.At(3, 2); got.Rune != 0 {
		t.Fatalf("expected wide glyph adjacent to the frame to be skipped entirely, got %+v", got)
	}
}

func containsRune(set []rune, r rune) bool {
	for _, s := range set {
		if s == r {
			return true
		}
	}
	return false
}

func TestFrameRendersTitleAndLines(t *testing.T) {
	f := NewFrame("node-1")
	f.Lines = []StatusLine{
		{Label: "uptime", Value: "3d"},
		{Label: "load", Value: "0.10 0.05 0.01"},
	}
	f.Layout(60, 20)

	g := NewGrid(60, 20)
	f.Render(g)
	snap := g.Snapshot()

	for _, want := range []string{"node-1", "uptime", "3d", "load", "0.10 0.05 0.01", defaultAlertOff} {
		if !strings.Contains(snap, want) {
			t.Errorf("expected %q in rendered frame:\n%s", want, snap)
		}
	}
}

// TestFrameStatusLinesRightJustifyValues covers the label-left/value-right
// table layout: each StatusLine's label starts at the frame's left text
// column and its value ends flush with the right text column.
func TestFrameStatusLinesRightJustifyValues(t *testing.T) {
	f := NewFrame("node-1")
	f.Lines = []StatusLine{
		{Label: "uptime", Value: "3d"},
		{Label: "load", Value: "0.10 0.05 0.01"},
	}
	f.Layout(60, 20)

	g := NewGrid(60, 20)
	f.Render(g)

	textX := f.X + 1 + framePadding
	textMaxX := f.X + f.W - 1 - framePadding
	textY := f.Y + 1 + framePadding

	for i, line := range f.Lines {
		y := textY + i
		if got := g.At(textX, y).Rune; got != rune(line.Label[0]) {
			t.Errorf("line %d: expected label %q to start at x=%d, got %q", i, line.Label, textX, got)
		}
		lastValueX := textMaxX - 1
		wantLastRune := rune(line.Value[len(line.Value)-1])
		if got := g.At(lastValueX, y).Rune; got != wantLastRune {
			t.Errorf("line %d: expected value %q to end at x=%d, got %q there", i, line.Value, lastValueX, got)
		}
	}
}

// TestFrameHasBlankRowBeforeAlert covers the visual separator between the
// status table and the alert indicator below it.
func TestFrameHasBlankRowBeforeAlert(t *testing.T) {
	f := NewFrame("node-1")
	f.Lines = []StatusLine{{Label: "uptime", Value: "3d"}}
	f.Layout(60, 20)

	g := NewGrid(60, 20)
	f.Render(g)

	textX := f.X + 1 + framePadding
	textMaxX := f.X + f.W - 1 - framePadding
	spacerY := f.Y + 1 + framePadding + len(f.Lines)

	for x := textX; x < textMaxX; x++ {
		if c := g.At(x, spacerY); c != (Cell{}) {
			t.Fatalf("expected blank spacer row at y=%d, found %+v at x=%d", spacerY, c, x)
		}
	}
}

func TestFrameAlertText(t *testing.T) {
	f := NewFrame("node-1")
	f.Alert = true
	f.Layout(60, 20)

	g := NewGrid(60, 20)
	f.Render(g)

	if !strings.Contains(g.Snapshot(), defaultAlertOn) {
		t.Error("expected alert-on text when Alert is set")
	}
}

func TestFrameTitleIsBold(t *testing.T) {
	f := NewFrame("myhost")
	f.Layout(40, 10)

	g := NewGrid(40, 10)
	f.Render(g)

	found := false
	for x := 0; x < 40; x++ {
		if c := g.At(x, f.Y); c.Rune == 'm' && c.Style == StyleFrameTitle {
			found = true
		}
	}
	if !found {
		t.Error("expected title characters styled StyleFrameTitle on the top border row")
	}
}

func TestScreenTickProducesExpectedANSIOutput(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := NewScreen(80, 24, "k8s-node-01", rng)
	s.SetStatus([]StatusLine{{Label: "uptime", Value: "1h2m"}, {Label: "load", Value: "0.01 0.02 0.03"}})
	s.SetAlert(true)
	s.Tick()

	out := s.Grid.ANSI()
	if !strings.HasPrefix(out, "\x1b[H") {
		t.Error("expected ANSI output to home the cursor first")
	}
	if !strings.Contains(out, "k8s-node-01") {
		t.Error("expected hostname in rendered ANSI output")
	}
	if !strings.Contains(out, defaultAlertOn) {
		t.Error("expected alert text in rendered ANSI output when alert is set")
	}
}
