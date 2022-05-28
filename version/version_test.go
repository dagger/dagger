package version

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShort(t *testing.T) {
	require.Equal(t, Short(), "dagger devel ()")
}

func TestLong(t *testing.T) {
	require.Equal(t, Long(), fmt.Sprintf("dagger devel () %s/%s", runtime.GOOS, runtime.GOARCH))
}
