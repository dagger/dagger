package scrub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScrubbers(t *testing.T) {
	// quick sanity check to make sure the regexes work, since regexes are hard
	for _, s := range scrubs {
		require.Regexp(t, s.re, s.sample)
	}
}
