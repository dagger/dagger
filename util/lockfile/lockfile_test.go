package lockfile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRejectsV1(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"sha256:3d23f8","float"]`,
	}, "\n")

	_, err := Parse([]byte(input))
	require.ErrorContains(t, err, `unsupported lockfile version "1"`)
}

func TestMarshalDeterministicOrdering(t *testing.T) {
	lock := New()
	require.NoError(t, lock.Set("b", "lookup", []any{"x"}, "r3"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"c", "d"}, "r1"))
	require.NoError(t, lock.Set("", "git.resolveRef", []any{"a", "b"}, "r2"))

	output, err := lock.Marshal()
	require.NoError(t, err)

	require.Equal(t, strings.Join([]string{
		`[["version","2"]]`,
		`["","git.resolveRef",["a","b"],"r2"]`,
		`["","git.resolveRef",["c","d"],"r1"]`,
		`["b","lookup",["x"],"r3"]`,
	}, "\n"), string(output))
}

func TestOptionsFollowValueAndAreCanonical(t *testing.T) {
	lock := New()
	inputs := []any{
		"registry.example/acme/image",
		[]any{
			[]any{"protocol", "https"},
			[]any{"insecureSkipTLSVerify", true},
		},
	}
	require.NoError(t, lock.Set("", "oci-latest", inputs, "2.0.0"))

	output, err := lock.Marshal()
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		`[["version","2"]]`,
		`["","oci-latest",["registry.example/acme/image"],"2.0.0",[["insecureSkipTLSVerify",true],["protocol","https"]]]`,
	}, "\n"), string(output))

	reparsed, err := Parse(output)
	require.NoError(t, err)
	value, ok := reparsed.Get("", "oci-latest", inputs)
	require.True(t, ok)
	require.Equal(t, "2.0.0", value)
}

func TestParseDuplicateTupleOverwrites(t *testing.T) {
	input := strings.Join([]string{
		`[["version","2"]]`,
		`["","oci-sha",["alpine:latest"],"old"]`,
		`["","oci-sha",["alpine:latest"],"new"]`,
	}, "\n")

	lock, err := Parse([]byte(input))
	require.NoError(t, err)

	value, ok := lock.Get("", "oci-sha", []any{"alpine:latest"})
	require.True(t, ok)
	require.Equal(t, "new", value)

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
		_, err := Parse([]byte(`[["version","3"]]`))
		require.Error(t, err)
		require.ErrorContains(t, err, "unsupported lockfile version")
	})

	t.Run("invalid tuple length", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","oci-sha"]`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid tuple length")
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`not-json`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid tuple JSON")
	})

	t.Run("unordered object input", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","git-sha",[{"ref":"main"}],"abc"]`,
		}, "\n")))
		require.Error(t, err)
		require.ErrorContains(t, err, "unordered object/map/dict in lock inputs")
	})

	t.Run("empty options are omitted", func(t *testing.T) {
		lock, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","oci-sha",["alpine:latest"],"sha256:abc",[]]`,
		}, "\n")))
		require.NoError(t, err)

		data, err := lock.Marshal()
		require.NoError(t, err)
		require.Equal(t, strings.Join([]string{
			`[["version","2"]]`,
			`["","oci-sha",["alpine:latest"],"sha256:abc"]`,
		}, "\n"), string(data))
	})

	t.Run("malformed options", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","oci-sha",["alpine:latest"],"sha256:abc",[["protocol"]]]`,
		}, "\n")))
		require.ErrorContains(t, err, "option must be a key-value pair")
	})

	t.Run("non-string value", func(t *testing.T) {
		_, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","oci-latest",["alpine"],true]`,
		}, "\n")))
		require.ErrorContains(t, err, "invalid value")
	})

	t.Run("grouped inputs", func(t *testing.T) {
		lock, err := Parse([]byte(strings.Join([]string{
			`[["version","2"]]`,
			`["","git-sha",["repo","main"],"abc"]`,
		}, "\n")))
		require.NoError(t, err)

		data, err := lock.Marshal()
		require.NoError(t, err)
		require.Equal(t, strings.Join([]string{
			`[["version","2"]]`,
			`["","git-sha",["repo","main"],"abc"]`,
		}, "\n"), string(data))
	})
}

func TestSetRejectsUnorderedInputObjects(t *testing.T) {
	lock := New()
	err := lock.Set("", "git.resolveRef", []any{map[string]any{"ref": "main"}}, "abc")
	require.Error(t, err)
	require.ErrorContains(t, err, "unordered object/map/dict in lock inputs")
}
