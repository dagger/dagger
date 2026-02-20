package schema

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
)

func TestParsePathWithFilters(t *testing.T) {
	var emptyFilter core.CopyFilter
	parse := func(value string, cf core.CopyFilter) (string, core.CopyFilter, error) {
		path, err := parsePathWithFilter(value, &cf)
		return path, cf, err
	}

	path, cf, err := parse("path", emptyFilter)
	require.NoError(t, err)
	require.Equal(t, "path", path)
	require.True(t, cf.IsEmpty(), "filter should be empty")

	path, cf, err = parse("path?include=a,b,c&exclude=x,y,z&gitignore", emptyFilter)
	require.NoError(t, err)
	require.Equal(t, "path", path)
	require.Equal(t, cf.Include, []string{"a", "b", "c"})
	require.Equal(t, cf.Exclude, []string{"x", "y", "z"})
	require.True(t, cf.Gitignore)

	path, cf, err = parse("path?include=d", cf)
	require.NoError(t, err)
	require.Equal(t, "path", path)
	require.Equal(t, cf.Include, []string{"a", "b", "c", "d"})
	require.Equal(t, cf.Exclude, []string{"x", "y", "z"})
	require.True(t, cf.Gitignore)

	_, cf, err = parse("path?gitignore=false", cf)
	require.NoError(t, err)
	require.False(t, cf.Gitignore)

	_, _, err = parse("path?", emptyFilter)
	require.Error(t, err)

	_, _, err = parse("path?gitignore=badvalue", emptyFilter)
	require.Error(t, err)

	_, _, err = parse("path?badoption=1", emptyFilter)
	require.Error(t, err)

	_, _, err = parse("path?badoption_with_novalue", emptyFilter)
	require.Error(t, err)
}
