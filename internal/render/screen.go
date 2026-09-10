package render

import "math/rand"

// Screen owns the whole visual state of nodematrix: the rain, the status
// frame, and the Grid the two are composited into. It is the type the
// daemon (or a demo tool) drives: create one, feed it status updates as
// they become known, call Tick on a regular schedule, and write Grid.ANSI()
// to wherever the frame should actually be displayed.
type Screen struct {
	Grid   *Grid
	Matrix *Matrix
	Frame  *Frame
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

// Tick advances the rain by one animation step and recomposites the grid in
// the required order: clear, draw rain everywhere except the frame's
// footprint, then draw the frame on top. Call Grid.ANSI() afterward to get
// the bytes to display.
func (s *Screen) Tick() {
	s.Matrix.Tick()
	s.Grid.Clear()
	s.Matrix.Render(s.Grid, s.Frame.Rect())
	s.Frame.Render(s.Grid)
}
