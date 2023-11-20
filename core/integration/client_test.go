package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientClose(t *testing.T) {
	t.Parallel()
	c, _ := connect(t)

	err := c.Close()
	require.NoError(t, err)
}
