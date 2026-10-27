package cleanups

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// RunEach runs the cleanups as Run does, in reverse, each once and with the
// name it was added with (a re-added one's at its new place), and runs the
// rest after one fails.
func TestRunEach(t *testing.T) {
	var c Cleanups
	var ran []string
	add := func(name string, err error) CleanupFunc {
		return c.Add(name, func() error {
			ran = append(ran, name)
			return err
		})
	}
	add("first", nil)
	later := add("re-added", nil)
	add("failing", errors.New("boom"))
	add("last", nil)
	c.ReAdd(later)

	var names []string
	err := c.RunEach(func(name string, cleanup CleanupF) error {
		names = append(names, name)
		return cleanup()
	})
	require.ErrorContains(t, err, "boom")
	require.Equal(t, []string{"re-added", "last", "failing", "re-added", "first"}, names)
	require.Equal(t, []string{"re-added", "last", "failing", "first"}, ran)
}
