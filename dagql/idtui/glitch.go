package idtui

import (
	"github.com/muesli/termenv"
)

// streamingTail renders the last few characters of an incomplete value
// with a fade-out gradient, visually indicating that more data is arriving.
// The tail shifts naturally as new characters stream in, creating motion
// without any time-based animation.
//
// For example, a streaming path "/src/main.g" renders as:
//
//	/src/mai  ← normal style
//	       n  ← slightly faded
//	        . ← more faded
//	         g ← most faded
//
// The style function receives the base value and a fade level (0 = no fade,
// higher = more faded) and should return the styled string.
func streamingTail(out TermOutput, value string, fg termenv.Color, faint bool) string {
	runes := []rune(value)
	n := len(runes)

	const tailLen = 3
	splitAt := n - tailLen
	if splitAt < 0 {
		splitAt = 0
	}

	head := string(runes[:splitAt])
	tail := runes[splitAt:]

	var result string

	// Render head with base style
	if head != "" {
		s := out.String(head)
		if fg != nil {
			s = s.Foreground(fg)
		}
		if faint {
			s = s.Faint()
		}
		result += s.String()
	}

	// Render tail characters with increasing fade
	// Use block shade characters interleaved with the actual chars
	shades := []string{"▓", "▒", "░"}
	for i, r := range tail {
		// How far into the tail (0 = least faded, tailLen-1 = most faded)
		depth := i - (len(tail) - min(len(tail), tailLen))

		s := out.String(string(r))
		if fg != nil {
			s = s.Foreground(fg)
		}
		s = s.Faint()
		result += s.String()

		// Append a shade block that gets lighter toward the end
		if depth < len(shades) {
			sh := out.String(shades[depth])
			if fg != nil {
				sh = sh.Foreground(fg)
			}
			result += sh.Faint().String()
		}
	}

	return result
}
