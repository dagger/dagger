package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// A call's result is logged whole up to maxLoggedResult; past that, its span
// logs the start and says how much is left out.
func TestLoggedResult(t *testing.T) {
	for _, n := range []int{0, 1, maxLoggedResult} {
		output := strings.Repeat("a", n)
		require.Equal(t, output, loggedResult(output), "a result of %d bytes is logged whole", n)
	}

	output := strings.Repeat("0123456789abcdef", 8<<10) // 128 KiB
	require.Equal(t,
		output[:maxLoggedResult]+"\n[65536 more bytes of the result are not logged]\n",
		loggedResult(output))

	// Cut where a character begins, and count every byte left out.
	output = "a" + strings.Repeat("é", maxLoggedResult) // 'é' is 2 bytes in UTF-8
	logged := loggedResult(output)
	head, marker, ok := strings.Cut(logged, "\n[")
	require.True(t, ok, logged[len(logged)-80:])
	require.True(t, utf8.ValidString(head))
	require.Equal(t, maxLoggedResult-1, len(head), "the cut moves back to the start of the character it falls in")
	require.Equal(t, output[:len(head)], head)
	require.Equal(t, "65538 more bytes of the result are not logged]\n", marker)
	require.Equal(t, len(output), len(head)+65538)

	// A head that already ends a line gets no blank line before the marker.
	output = strings.Repeat("a", maxLoggedResult-1) + "\nmore"
	require.Equal(t,
		output[:maxLoggedResult]+"[4 more bytes of the result are not logged]\n",
		loggedResult(output))
}
