package render

import "math/rand"

// The rain draws from three pools of characters, mixed by weighted chance
// each time a glyph needs picking (a new column head, or an existing trail
// character "flickering" to something else):
//
//   - asciiGlyphs: ordinary characters, the bulk of cmatrix's own default
//     character set.
//   - kanaGlyphs: halfwidth Japanese katakana (Unicode block U+FF66-FF9F),
//     included for visual authenticity — this is in fact the same character
//     set the original Matrix films' "code rain" effect was built from.
//     Deliberately *halfwidth* rather than the more common full-width
//     katakana/hiragana forms: full-width characters render as two terminal
//     columns wide, which would silently throw off the column alignment of
//     everything to their right (this bit us during development - see
//     Cell/wide handling in matrix.go). Halfwidth katakana is guaranteed
//     single-column everywhere. True hiragana has no halfwidth Unicode form
//     at all, so it isn't included here for the same reason.
//   - emojiGlyphs: a small, deliberately short list of double-width bonus
//     characters. Currently disabled (see weightEmoji) — being the only
//     double-width glyphs in an otherwise all-single-width rain, they stood
//     out visually rather than blending in. The pool and the
//     continuation-marker handling that makes double-width glyphs safe
//     (matrix.go/ansi.go) are kept in place in case wide glyphs are wanted
//     again later; re-enabling is just a matter of giving weightEmoji a
//     non-zero value.
//
// weightAscii+weightKana+weightEmoji does not need to sum to any particular
// value; randomGlyph below treats them as relative weights.
var (
	asciiGlyphs = []rune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ!\"#$%&'()*+-.:;<=>?@[]^_`{|}~")

	// U+FF66-FF9F: halfwidth katakana letters and sound marks.
	kanaGlyphs = buildKanaGlyphs()

	emojiGlyphs = []rune{
		'⚡', '💀', '👾', '🔥', '👁', '🐇', '🧠', '💊',
	}
)

func buildKanaGlyphs() []rune {
	var kana []rune
	for r := rune(0xFF66); r <= 0xFF9F; r++ {
		kana = append(kana, r)
	}
	return kana
}

const (
	weightASCII = 55
	weightKana  = 44
	// weightEmoji is 0: emoji are disabled for now (see the doc comment
	// above). Give it a non-zero value to bring them back.
	weightEmoji = 0
)

// randomGlyph picks a single rain character using rng, drawing mostly from
// ASCII and halfwidth katakana with an occasional emoji thrown in. It
// reports both the rune and whether it is double-width on screen, since the
// caller (matrix.go) needs to know that to keep column alignment correct.
func randomGlyph(rng *rand.Rand) (r rune, wide bool) {
	switch n := rng.Intn(weightASCII + weightKana + weightEmoji); {
	case n < weightASCII:
		return asciiGlyphs[rng.Intn(len(asciiGlyphs))], false
	case n < weightASCII+weightKana:
		return kanaGlyphs[rng.Intn(len(kanaGlyphs))], false
	default:
		return emojiGlyphs[rng.Intn(len(emojiGlyphs))], true
	}
}
