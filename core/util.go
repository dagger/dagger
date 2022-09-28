package core

import (
	"strings"

	"github.com/pkg/errors"
)

// ErrNotImplementedYet is used to stub out API fields that aren't implemented
// yet.
var ErrNotImplementedYet = errors.New("not implemented yet")

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
