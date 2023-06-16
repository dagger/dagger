package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTP(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	// do two in a row to ensure each gets downloaded correctly
	url := "https://raw.githubusercontent.com/dagger/dagger/main/TESTING.md"
	contents, err := c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "tests")

	url = "https://raw.githubusercontent.com/dagger/dagger/main/README.md"
	contents, err = c.HTTP(url).Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "Dagger")
}
