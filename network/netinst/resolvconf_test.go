//go:build linux
// +build linux

package netinst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteResolverConfigKeepsInternalResolverAndPublicNameservers(t *testing.T) {
	t.Parallel()

	src := strings.NewReader(strings.Join([]string{
		"nameserver 10.0.0.10",
		"nameserver 127.0.0.1",
		"nameserver 8.8.4.4",
		"search example.com",
	}, "\n"))

	var dst strings.Builder
	require.NoError(t, writeResolverConfig(&dst, src, "10.0.0.2"))

	got := dst.String()
	require.Contains(t, got, "nameserver 10.0.0.2")
	require.Contains(t, got, "nameserver 10.0.0.10")
	require.Contains(t, got, "nameserver 8.8.4.4")
	require.NotContains(t, got, "nameserver 127.0.0.1")
	require.Contains(t, got, "search example.com")
}

func TestReplaceNameserversWritesFallbackWhenNoSourceNameservers(t *testing.T) {
	t.Parallel()

	srcFile := filepath.Join(t.TempDir(), "resolv.conf")
	require.NoError(t, os.WriteFile(srcFile, []byte("search example.com\n"), 0600))

	var dst strings.Builder
	src, err := os.Open(srcFile)
	require.NoError(t, err)
	defer src.Close()

	require.NoError(t, writeResolverConfig(&dst, src, "10.0.0.2"))
	got := dst.String()
	require.Contains(t, got, "nameserver 10.0.0.2")
	require.Contains(t, got, "nameserver 1.1.1.1")
	require.Contains(t, got, "nameserver 8.8.8.8")
}
