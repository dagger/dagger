package tui

import (
	"strings"

	"golang.org/x/exp/constraints"
)

func max[T constraints.Ordered](i, j T) T {
	if i > j {
		return i
	}
	return j
}

func min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func trunc(str string, size int) string {
	if len(str) <= size {
		return str
	}

	return str[:size-1] + "…"
}

func sanitizeFilename(name string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return ' '
		default:
			return r
		}
	}, name)
	return strings.Join(strings.Fields(sanitized), " ") + ".log"
}
