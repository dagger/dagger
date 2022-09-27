package core

import (
	"strings"
)

func truncate(s string, lines *int) string {
	if lines == nil {
		return s
	}
	l := strings.SplitN(s, "\n", *lines+1)
	if *lines > len(l) {
		*lines = len(l)
	}
	return strings.Join(l[0:*lines], "\n")
}
