package render

import (
	"strings"
	"testing"
)

func TestDiffRendererFirstCallIsFullDraw(t *testing.T) {
	g := NewGrid(5, 3)
	g.Set(1, 1, Cell{Rune: 'x', Style: StyleTrail})

	d := NewDiffRenderer()
	got := d.Render(g)
	want := g.ANSI()
	if got != want {
		t.Errorf("expected first Render call to equal a full Grid.ANSI() draw\ngot:  %q\nwant: %q", got, want)
	}
}

func TestDiffRendererNoChangeProducesEmptyOutput(t *testing.T) {
	g := NewGrid(5, 3)
	g.Set(1, 1, Cell{Rune: 'x', Style: StyleTrail})

	d := NewDiffRenderer()
	d.Render(g) // first call: full draw, establishes baseline

	if got := d.Render(g); got != "" {
		t.Errorf("expected empty output for an unchanged grid, got %q", got)
	}
}

func TestDiffRendererSingleCellChange(t *testing.T) {
	g := NewGrid(5, 3)
	d := NewDiffRenderer()
	d.Render(g) // baseline: all blank

	g.Set(2, 1, Cell{Rune: 'x', Style: StyleTrail})
	got := d.Render(g)

	want := "\x1b[2;3H" + ansiCodes[StyleTrail] + "x" + "\x1b[0m"
	if got != want {
		t.Errorf("single-cell change:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestDiffRendererAdjacentChangesDoNotRepositionCursor(t *testing.T) {
	g := NewGrid(5, 3)
	d := NewDiffRenderer()
	d.Render(g)

	g.Set(1, 0, Cell{Rune: 'a', Style: StyleTrail})
	g.Set(2, 0, Cell{Rune: 'b', Style: StyleTrail})
	got := d.Render(g)

	// One positioning escape for the run, not one per cell (the cursor
	// naturally advances after printing 'a'), and one style escape, reused
	// for 'b' rather than re-emitted.
	want := "\x1b[1;2H" + ansiCodes[StyleTrail] + "ab" + "\x1b[0m"
	if got != want {
		t.Errorf("adjacent same-style run:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestDiffRendererNonAdjacentChangesEachRepositioned(t *testing.T) {
	g := NewGrid(10, 3)
	d := NewDiffRenderer()
	d.Render(g)

	g.Set(1, 0, Cell{Rune: 'a', Style: StyleTrail})
	g.Set(8, 0, Cell{Rune: 'b', Style: StyleTrail})
	got := d.Render(g)

	if !strings.Contains(got, "\x1b[1;2H") {
		t.Errorf("expected a positioning escape for the first change: %q", got)
	}
	if !strings.Contains(got, "\x1b[1;9H") {
		t.Errorf("expected a positioning escape for the second, non-adjacent change: %q", got)
	}
}

func TestDiffRendererSkipsRedundantStyleCodesAcrossAJump(t *testing.T) {
	g := NewGrid(10, 3)
	d := NewDiffRenderer()
	d.Render(g)

	g.Set(1, 0, Cell{Rune: 'a', Style: StyleTrail})
	g.Set(8, 0, Cell{Rune: 'b', Style: StyleTrail}) // same style as the first change, despite the jump
	got := d.Render(g)

	if n := strings.Count(got, ansiCodes[StyleTrail]); n != 1 {
		t.Errorf("expected the StyleTrail escape to be emitted once and reused across the cursor jump, found %d times in %q", n, got)
	}
}

func TestDiffRendererHandlesResize(t *testing.T) {
	g := NewGrid(5, 3)
	d := NewDiffRenderer()
	d.Render(g)

	bigger := NewGrid(6, 3)
	bigger.Set(0, 0, Cell{Rune: 'z', Style: StyleTrail})
	got := d.Render(bigger)

	if got != bigger.ANSI() {
		t.Errorf("expected a full redraw when grid dimensions change:\ngot:  %q\nwant: %q", got, bigger.ANSI())
	}
}

func TestDiffRendererResetForcesFullDraw(t *testing.T) {
	g := NewGrid(5, 3)
	d := NewDiffRenderer()
	d.Render(g)
	d.Reset()

	got := d.Render(g)
	want := g.ANSI()
	if got != want {
		t.Errorf("expected a full redraw after Reset:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestDiffRendererWideGlyphContinuationHandling covers the interaction
// between double-width glyphs and the diff/cursor bookkeeping: a wide
// glyph's continuation marker must never be visited directly, and the
// cursor position tracking must account for the extra column it consumes.
// Cells are set directly (rather than via Matrix.Render) to keep this
// deterministic and isolated to exactly the change under test.
func TestDiffRendererWideGlyphContinuationHandling(t *testing.T) {
	const width, height = 10, 3
	g := NewGrid(width, height)
	d := NewDiffRenderer()
	d.Render(g) // baseline: blank

	wide := emojiGlyphs[0]
	g.Set(3, 1, Cell{Rune: wide, Style: StyleHead})
	g.Set(4, 1, Cell{Rune: continuationRune, Style: StyleNone})
	// Also change the cell immediately after the continuation marker, to
	// confirm the cursor bookkeeping correctly treats the terminal as
	// already positioned there (no redundant escape) after the wide glyph.
	g.Set(5, 1, Cell{Rune: 'z', Style: StyleTrail})

	got := d.Render(g)

	if !strings.Contains(got, string(emojiGlyphs[0])) {
		t.Fatalf("expected the wide glyph itself in the output: %q", got)
	}
	if strings.Contains(got, string(continuationRune)) {
		t.Fatalf("continuation marker must never be written as visible output: %q", got)
	}
	// Only one positioning escape expected: to (3,1) for the wide glyph.
	// The trailing 'z' at (5,1) should follow with no extra positioning
	// escape, since printing the wide glyph already left the cursor at
	// column 5 (0-indexed x=4's continuation, x=5 next).
	if n := strings.Count(got, "\x1b[2;4H"); n != 1 {
		t.Errorf("expected exactly one positioning escape to the wide glyph's cell, found %d in %q", n, got)
	}
	if strings.Contains(got, "\x1b[2;6H") {
		t.Errorf("did not expect a positioning escape for (5,1); the cursor should already be there after the wide glyph: %q", got)
	}
}
