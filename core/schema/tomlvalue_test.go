package schema

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestTOMLSourceToDataRoundTrip(t *testing.T) {
	src := []byte(`
# a comment
title = "example"
count = 3

[owner]
name = "Alice"
active = true
`)
	data, err := tomlSourceToData(src)
	require.NoError(t, err)
	require.JSONEq(t, `{"title":"example","count":3,"owner":{"name":"Alice","active":true}}`, string(data))
}

func TestTOMLDecodeDataPreservesIntegers(t *testing.T) {
	v, err := tomlDecodeData([]byte(`{"count":3,"ratio":1.5}`))
	require.NoError(t, err)
	m := v.(map[string]any)
	require.Equal(t, int64(3), m["count"])
	require.Equal(t, 1.5, m["ratio"])
}

func TestTOMLEncodeTable(t *testing.T) {
	out, err := tomlEncode(map[string]any{"name": "Bob", "count": int64(2)})
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "Bob"`)
	require.Contains(t, string(out), `count = 2`)
}

func TestTOMLEncodeRejectsScalar(t *testing.T) {
	_, err := tomlEncode("nope")
	require.Error(t, err)
}

func TestTOMLDottedPathQuoting(t *testing.T) {
	require.Equal(t, "owner.name", tomlDottedPath([]dagql.String{"owner", "name"}))
	require.Equal(t, `owner."full name"`, tomlDottedPath([]dagql.String{"owner", "full name"}))
	require.Equal(t, `"with.dot"`, tomlDottedPath([]dagql.String{"with.dot"}))
}

// TestTOMLApplyFieldPreservesFormatting verifies that editing an existing
// document keeps comments and surrounding formatting intact.
func TestTOMLApplyFieldPreservesFormatting(t *testing.T) {
	src := []byte(`# top comment
title = "old"  # inline comment

[owner]
name = "Alice"
`)
	rootMap := map[string]any{
		"title": "new",
		"owner": map[string]any{"name": "Alice"},
	}
	out, err := tomlApplyField(src, []dagql.String{"title"}, []byte(`"new"`), rootMap)
	require.NoError(t, err)
	got := string(out)
	require.Contains(t, got, "# top comment")
	require.Contains(t, got, "# inline comment")
	require.Contains(t, got, "[owner]")
	require.Contains(t, got, `title = "new"`)
	require.NotContains(t, got, `title = "old"`)
}

func TestTOMLApplyFieldAddsNestedField(t *testing.T) {
	src := []byte("title = \"x\"\n")
	rootMap := map[string]any{
		"title":   "x",
		"profile": map[string]any{"email": "bob@example.com"},
	}
	out, err := tomlApplyField(src, []dagql.String{"profile", "email"}, []byte(`"bob@example.com"`), rootMap)
	require.NoError(t, err)
	got := string(out)
	require.Contains(t, got, `title = "x"`)
	require.Contains(t, got, "bob@example.com")
}

func TestTOMLApplyFieldNoSource(t *testing.T) {
	out, err := tomlApplyField(nil, []dagql.String{"a"}, []byte(`1`), map[string]any{"a": int64(1)})
	require.NoError(t, err)
	require.Nil(t, out)
}

// TestTOMLApplyFieldEmptySource exercises the fresh-document path: a TOMLValue
// created by the `toml` constructor carries an empty (but non-nil) Source, and
// editing it should produce a valid document. This is the starting point the
// withField/contents integration tests rely on.
func TestTOMLApplyFieldEmptySource(t *testing.T) {
	out, err := tomlApplyField([]byte{}, []dagql.String{"name"}, []byte(`"Alice"`), map[string]any{"name": "Alice"})
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "Alice"`)

	// A nested field on the fresh document creates the intermediate table.
	out, err = tomlApplyField([]byte{}, []dagql.String{"profile", "email"}, []byte(`"bob@example.com"`), map[string]any{
		"profile": map[string]any{"email": "bob@example.com"},
	})
	require.NoError(t, err)
	require.Contains(t, string(out), "bob@example.com")
	// Round-trips back to the same value model.
	data, err := tomlSourceToData(out)
	require.NoError(t, err)
	require.JSONEq(t, `{"profile":{"email":"bob@example.com"}}`, string(data))
}

// TestTOMLApplyFieldPreservesIntegerType guards against integers being widened
// to floats when an edit re-encodes scalar values.
func TestTOMLApplyFieldPreservesIntegerType(t *testing.T) {
	src := []byte("count = 1\n")
	out, err := tomlApplyField(src, []dagql.String{"count"}, []byte(`3`), map[string]any{"count": int64(3)})
	require.NoError(t, err)
	require.Contains(t, string(out), "count = 3")
	require.NotContains(t, string(out), "count = 3.0")
}
