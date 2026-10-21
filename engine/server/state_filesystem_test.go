package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFdinfoMountID(t *testing.T) {
	parse := func(data string) (int, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fdinfo")
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		return parseFdinfoMountID(f)
	}
	id, err := parse("pos:\t0\nflags:\t0100000\nmnt_id:\t1234\nino:\t42\n")
	require.NoError(t, err)
	require.Equal(t, 1234, id)
	_, err = parse("pos:\t0\nflags:\t0100000\n")
	require.ErrorContains(t, err, "no mnt_id")
}

func TestStateMountOfTempDir(t *testing.T) {
	dir := t.TempDir()
	m, err := stateMount(dir)
	require.NoError(t, err)
	// The kernel's answer is a mount that contains the directory.
	rel, err := filepath.Rel(m.Mountpoint, dir)
	require.NoError(t, err)
	require.False(t, rel == ".." || strings.HasPrefix(rel, "../"), "mount %s doesn't contain %s", m.Mountpoint, dir)
}
