package idtui

// glitchRunes are characters used to create a cyberpunk streaming indicator.
var glitchRunes = []rune{
	'░', '▒', '▓', '█',
	'╌', '╍', '┄', '┅',
	'⡀', '⠁', '⠂', '⠄', '⡁', '⠅',
	'▖', '▗', '▘', '▝', '▞', '▟',
	'⣀', '⣤', '⣶', '⣿',
}

// streamingGlitch returns a single glitch character derived from the
// content itself. It changes whenever the content changes but stays
// stable across re-renders of the same content.
func streamingGlitch(content string) string {
	// Simple hash: sum all bytes
	var h uint
	for i := range len(content) {
		h = h*31 + uint(content[i])
	}
	return string(glitchRunes[h%uint(len(glitchRunes))])
}
