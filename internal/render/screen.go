package render

import "math/rand"

// Screen owns the whole visual state of nodematrix: the rain, the status
// frame, and the Grid the two are composited into. It is the type the
// daemon (or a demo tool) drives: create one, feed it status updates as
// they become known, call Tick on a regular schedule, and write ANSI() to
// wherever the frame should actually be displayed.
type Screen struct {
	Grid   *Grid
	Matrix *Matrix
	Frame  *Frame

	diff *DiffRenderer
}

// NewScreen builds a Screen sized width x height with a status frame
// titled hostname. rng seeds the rain's randomness — pass a
// deterministically-seeded *rand.Rand in tests for reproducible output, or
// one seeded from real entropy in production. kanaEnabled is forwarded to
// NewMatrix - see its doc comment for why this defaults to false.
func NewScreen(width, height int, hostname string, rng *rand.Rand, kanaEnabled bool) *Screen {
	f := NewFrame(hostname)
	f.Layout(width, height)
	return &Screen{
		Grid:   NewGrid(width, height),
		Matrix: NewMatrix(width, height, rng, kanaEnabled),
		Frame:  f,
		diff:   NewDiffRenderer(),
	}
}

// SetStatus replaces the frame's status rows. It re-runs layout since the
// frame's size depends on its longest row. Call it periodically with fresh
// values (the daemon's intended cadence is every few seconds - status
// content like uptime/load/memory doesn't need updating anywhere near as
// often as the rain animates) rather than on every Tick.
func (s *Screen) SetStatus(lines []StatusLine) {
	s.Frame.Lines = lines
	s.Frame.Layout(s.Grid.Width, s.Grid.Height)
}

// SetAlert sets whether the frame shows the "console activity behind the
// screensaver" indicator.
func (s *Screen) SetAlert(alert bool) {
	s.Frame.Alert = alert
}

// Resize rebuilds the screen for a new width x height - e.g. after
// discovering the console's actual character grid no longer matches what
// this Screen was built for (see the daemon's periodic and per-show VT size
// checks). The rain restarts fresh - there's no sane way to resample
// mid-fall columns onto a different height - but the current title, status
// lines, and alert state carry over rather than reset, since a resize isn't
// a new show session. rng continues the same random stream NewScreen was
// given rather than starting a new one. As with ForceFullRedraw, the next
// ANSI() call does a full redraw, since there's no previous frame at the
// new size to diff against.
func (s *Screen) Resize(width, height int, rng *rand.Rand) {
	title := s.Frame.Title
	lines := s.Frame.Lines
	alert := s.Frame.Alert
	kanaEnabled := s.Matrix.kanaEnabled

	s.Grid = NewGrid(width, height)
	s.Matrix = NewMatrix(width, height, rng, kanaEnabled)
	s.Frame = NewFrame(title)
	s.Frame.Lines = lines
	s.Frame.Alert = alert
	s.Frame.Layout(width, height)
	s.diff = NewDiffRenderer()
}

// ForceFullRedraw discards the diff renderer's cached idea of what the
// terminal currently displays, so the next ANSI() call does a full redraw
// instead of emitting only changed cells. The diff cache only reflects what
// this package itself last wrote - it has no way to notice the terminal
// being changed by anything else with write access to the same device (a
// stray getty, a console message that lands there despite console=
// pinning, ...), so without an occasional forced resync any such change
// sticks around indefinitely instead of just until the next frame. Callers
// should invoke this both when a show session starts (to clear out
// corruption accumulated while hidden) and periodically during a long show
// session (to recover from corruption introduced while displaying).
func (s *Screen) ForceFullRedraw() {
	s.diff.Reset()
}

// Tick advances the rain by one animation step and recomposites the grid in
// the required order: clear, draw rain everywhere except the frame's
// footprint, then draw the frame on top. Call ANSI() afterward to get the
// bytes to display.
func (s *Screen) Tick() {
	s.Matrix.Tick()
	s.Grid.Clear()
	s.Matrix.Render(s.Grid, s.Frame.Rect())
	s.Frame.Render(s.Grid)
}

// ANSI returns the bytes needed to bring the terminal from whatever it
// displayed after the previous call to ANSI (or an unknown state, on the
// first call) to match the screen's current state - see DiffRenderer. This
// is the normal way to get output to write to a real terminal/console
// device; Grid.ANSI (a full, unconditional redraw) remains available
// directly on the Grid for callers that specifically want that instead.
func (s *Screen) ANSI() string {
	return s.diff.Render(s.Grid)
}
