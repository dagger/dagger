package idtui

import (
	"math/rand"
	"strings"
)

// glitchRunes are characters used to create a cyberpunk streaming indicator.
// A mix of unicode block elements, box-drawing fragments, and braille dots
// that evoke a garbled/decoding feel.
var glitchRunes = []rune{
	'░', '▒', '▓', '█',
	'╌', '╍', '┄', '┅',
	'⡀', '⠁', '⠂', '⠄', '⡁', '⠅',
	'▖', '▗', '▘', '▝', '▞', '▟',
	'⣀', '⣤', '⣶', '⣿',
}

// glitchText returns a short string of random glitch characters.
// The length varies between 2 and 4 characters. Each call produces
// different output, creating an animated effect when re-rendered.
func glitchText(n int) string {
	if n <= 0 {
		n = 2 + rand.Intn(3) //nolint:gosec
	}
	var sb strings.Builder
	for range n {
		sb.WriteRune(glitchRunes[rand.Intn(len(glitchRunes))]) //nolint:gosec
	}
	return sb.String()
}
