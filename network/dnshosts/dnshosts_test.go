package dnshosts

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addnhosts")
	require.NoError(t, os.WriteFile(path, []byte(
		"10.87.0.5\tweb\talias\n"+
			"10.87.0.7\tdb\n"+
			"10.87.0.9\tweb\n"+
			"fd00::5\tweb\n"+
			"10.87.0.5\tdb\n",
	), 0o644))

	found, err := Remove(path, "web", []net.IP{net.ParseIP("10.87.0.5"), net.ParseIP("fd00::5")})
	require.NoError(t, err)
	require.True(t, found)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "10.87.0.7\tdb\n10.87.0.9\tweb\n10.87.0.5\tdb\n", string(got))

	found, err = Remove(path, "web", []net.IP{net.ParseIP("10.87.0.5")})
	require.NoError(t, err)
	require.False(t, found, "entries at other addresses are kept")
}
