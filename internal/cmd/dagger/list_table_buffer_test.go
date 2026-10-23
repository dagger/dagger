package daggercmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Each write to the CLI's output produces one telemetry log record. A large
// table must not flood the frontend's 2048-record queue with cells and padding.
func TestArtifactTableLargeOutput(t *testing.T) {
	const count = 1000
	items := make([]listedArtifact, count)
	for i := range items {
		items[i] = listedArtifact{
			URI: fmt.Sprintf("dag+check://go/packages/test?go-package=package-%04d", i),
			DimensionKeys: []struct{ Dimension, Key string }{
				{"go/packages", fmt.Sprintf("package-%04d", i)},
				{"type:Check", "test"},
			},
		}
	}
	var output bytes.Buffer
	writes := 0
	err := writeArtifactTable(artifactTableWriterFunc(func(p []byte) (int, error) {
		writes++
		return output.Write(p)
	}), items, map[string]string{"go/packages": "go-package", "type:Check": "check"})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	require.Len(t, lines, count+1)
	require.Equal(t, []string{"GO-PACKAGE", "CHECK"}, strings.Fields(lines[0]))
	for i, line := range lines[1:] {
		require.Equal(t, []string{fmt.Sprintf("package-%04d", i), "test"}, strings.Fields(line))
	}
	require.Less(t, writes, 2048, "table must fit within the default telemetry log queue")
	t.Logf("%d table rows produced %d output records (%d bytes)", count, writes, output.Len())
}

func TestArtifactTableOutputError(t *testing.T) {
	expected := errors.New("output unavailable")
	out := artifactTableWriterFunc(func([]byte) (int, error) { return 0, expected })
	require.ErrorIs(t, writeArtifactTable(out, nil, nil), expected)
}

type artifactTableWriterFunc func([]byte) (int, error)

func (f artifactTableWriterFunc) Write(p []byte) (int, error) { return f(p) }
