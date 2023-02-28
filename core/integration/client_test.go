package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientClose(t *testing.T) {
	c, _ := connect(t)

	err := c.Close()
	require.NoError(t, err)
}
