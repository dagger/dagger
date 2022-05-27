package version

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShort(t *testing.T) {
	output := Short()
	require.Equal(t, output, "dagger devel ()")
}

func TestLong(t *testing.T) {
	output := Long()
	require.Equal(t, output, fmt.Sprintf("dagger devel () %s/%s", runtime.GOOS, runtime.GOARCH))
}
