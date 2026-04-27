package lockfile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMarshalRoundTrip(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"sha256:3d23f8","float"]`,
		`["github.com/acme/release","lookupVersion",["stable"],"v1.2.3","float"]`,
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

	value, policy, ok := reparsed.Get("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.True(t, ok)
	require.Equal(t, "sha256:3d23f8", value)
	require.Equal(t, "float", policy)
}

func TestMarshalDeterministicOrdering(t *testing.T) {
	lock := New()
	require.NoError(t, lock.Set("b", "lookup", []any{"x"}, "r3", "float"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"c", "d"}, "r1", "pin"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"a", "b"}, "r2", "pin"))

	output, err := lock.Marshal()
	require.NoError(t, err)

	require.Equal(t, strings.Join([]string{
		`[["version","1"]]`,
		`["","git.resolveRef",["a","b"],"r2","pin"]`,
		`["","git.resolveRef",["c","d"],"r1","pin"]`,
		`["b","lookup",["x"],"r3","float"]`,
	}, "\n"), string(output))
}

func TestParseDuplicateTupleOverwrites(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"old","float"]`,
		`["","container.from",["alpine:latest","linux/amd64"],"new","float"]`,
	}, "\n")

	lock, err := Parse([]byte(input))
	require.NoError(t, err)

	value, policy, ok := lock.Get("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.True(t, ok)
	require.Equal(t, "new", value)
	require.Equal(t, "float", policy)

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
		_, err := Parse([]byte(`["","container.from",["alpine:latest"],"abc","float"]`))
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
			`["","container.from",["alpine:latest"]]`,
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

	t.Run("unordered object input", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","1"]]`,
			`["","git.resolveRef",[{"ref":"main"}],"abc","pin"]`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "unordered object/map/dict in lock inputs")
	})

	t.Run("unordered object value", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","1"]]`,
			`["","git.resolveRef",["main"],{"sha":"abc"},"pin"]`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "unordered object/map/dict in lock value")
	})
}

func TestSetRejectsUnorderedInputObjects(t *testing.T) {
	lock := New()
	err := lock.Set("", "git.resolveRef", []any{map[string]any{"ref": "main"}}, "abc", "pin")
	require.Error(t, err)
	require.ErrorContains(t, err, "unordered object/map/dict in lock inputs")
}

func TestParseRejectsLegacyResultEnvelope(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],{"value":"sha256:3d23f8","policy":"float"}]`,
	}, "\n")

	_, err := Parse([]byte(input))
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid tuple length 4: expected 5")
}
