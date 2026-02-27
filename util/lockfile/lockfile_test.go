package lockfile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMarshalRoundTrip(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","modules.resolve",["github.com/dagger/go-toolchain@v1.0"],{"value":"3d23f8","policy":"pin"}]`,
		`["github.com/acme/release","lookupVersion",["stable"],{"value":"v1.2.3","policy":"float"}]`,
	}, "\n")

	parsed, err := Parse([]byte(input))
	require.NoError(t, err)

	output, err := parsed.Marshal()
	require.NoError(t, err)

	reparsed, err := Parse(output)
	require.NoError(t, err)

	output2, err := reparsed.Marshal()
	require.NoError(t, err)
	require.Equal(t, string(output), string(output2))

	result, ok := reparsed.Get("", "modules.resolve", []any{"github.com/dagger/go-toolchain@v1.0"})
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"value":  "3d23f8",
		"policy": "pin",
	}, result)
}

func TestMarshalDeterministicOrdering(t *testing.T) {
	lock := New()
	require.NoError(t, lock.Set("b", "lookup", []any{"x"}, "r3"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"c", "d"}, "r1"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"a", "b"}, "r2"))

	output, err := lock.Marshal()
	require.NoError(t, err)

	require.Equal(t, strings.Join([]string{
		`[["version","1"]]`,
		`["","git.resolveRef",["a","b"],"r2"]`,
		`["","git.resolveRef",["c","d"],"r1"]`,
		`["b","lookup",["x"],"r3"]`,
	}, "\n"), string(output))
}

func TestParseDuplicateTupleOverwrites(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","modules.resolve",["github.com/acme/dep@main"],{"value":"old","policy":"pin"}]`,
		`["","modules.resolve",["github.com/acme/dep@main"],{"value":"new","policy":"pin"}]`,
	}, "\n")

	lock, err := Parse([]byte(input))
	require.NoError(t, err)

	result, ok := lock.Get("", "modules.resolve", []any{"github.com/acme/dep@main"})
	require.True(t, ok)
	require.Equal(t, map[string]any{"value": "new", "policy": "pin"}, result)

	output, err := lock.Marshal()
	require.NoError(t, err)
	require.Equal(t, 2, len(strings.Split(string(output), "\n")))
	require.Contains(t, string(output), `"new"`)
	require.NotContains(t, string(output), `"old"`)
}

func TestParseMalformedAndEmpty(t *testing.T) {
	t.Run("empty file", func(t *testing.T) {
		lock, err := Parse(nil)
		require.NoError(t, err)

		output, err := lock.Marshal()
		require.NoError(t, err)
		require.Empty(t, output)
	})

	t.Run("missing header", func(t *testing.T) {
		_, err := Parse([]byte(`["","modules.resolve",["dep"],{"value":"abc","policy":"pin"}]`))
		require.Error(t, err)
		require.ErrorContains(t, err, "missing version header")
	})

	t.Run("unsupported version", func(t *testing.T) {
		_, err := Parse([]byte(`[["version","2"]]`))
		require.Error(t, err)
		require.ErrorContains(t, err, "unsupported lockfile version")
	})

	t.Run("invalid tuple length", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","1"]]`,
			`["","modules.resolve",["dep"]]`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid tuple length")
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","1"]]`,
			`not-json`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid tuple JSON")
	})

	t.Run("object input is normalized to ordered pairs", func(t *testing.T) {
		lock, err := Parse([]byte(strings.Join([]string{
			`[["version","1"]]`,
			`["","git.resolveRef",[{"b":"2","a":"1"}],"abc"]`,
		}, "\n")))
		require.NoError(t, err)

		output, err := lock.Marshal()
		require.NoError(t, err)
		require.Contains(t, string(output), `["","git.resolveRef",[[["a","1"],["b","2"]]],"abc"]`)
	})
}

func TestSetNormalizesObjectInputsToOrderedPairs(t *testing.T) {
	lock := New()
	require.NoError(t, lock.Set("", "git.resolveRef", []any{map[string]any{"b": "2", "a": "1"}}, "abc"))

	output, err := lock.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(output), `["","git.resolveRef",[[["a","1"],["b","2"]]],"abc"]`)

	result, ok := lock.Get("", "git.resolveRef", []any{map[string]any{"a": "1", "b": "2"}})
	require.True(t, ok)
	require.Equal(t, "abc", result)
}
