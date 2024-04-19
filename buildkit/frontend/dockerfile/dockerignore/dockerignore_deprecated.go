package dockerignore

import (
	"io"

	"github.com/moby/patternmatcher/ignorefile"
)

// ReadAll is an alias for [ignorefile.ReadAll].
//
// Deprecated: use [ignorefile.ReadAll] instead.
func ReadAll(reader io.Reader) ([]string, error) {
	return ignorefile.ReadAll(reader)
}
