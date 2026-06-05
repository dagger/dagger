package schema

import (
	"math"
	"testing"
	"time"

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
	out, err := tomlApplyField(src, []dagql.String{"title"}, "new", rootMap)
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
	out, err := tomlApplyField(src, []dagql.String{"profile", "email"}, "bob@example.com", rootMap)
	require.NoError(t, err)
	got := string(out)
	require.Contains(t, got, `title = "x"`)
	require.Contains(t, got, "bob@example.com")
}

func TestTOMLApplyFieldNoSource(t *testing.T) {
	out, err := tomlApplyField(nil, []dagql.String{"a"}, int64(1), map[string]any{"a": int64(1)})
	require.NoError(t, err)
	require.Nil(t, out)
}

// TestTOMLApplyFieldEmptySource exercises the fresh-document path: a TOMLValue
// created by the `toml` constructor carries an empty (but non-nil) Source, and
// editing it should produce a valid document. This is the starting point the
// withField/contents integration tests rely on.
func TestTOMLApplyFieldEmptySource(t *testing.T) {
	out, err := tomlApplyField([]byte{}, []dagql.String{"name"}, "Alice", map[string]any{"name": "Alice"})
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "Alice"`)

	// A nested field on the fresh document creates the intermediate table.
	out, err = tomlApplyField([]byte{}, []dagql.String{"profile", "email"}, "bob@example.com", map[string]any{
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
	out, err := tomlApplyField(src, []dagql.String{"count"}, int64(3), map[string]any{"count": int64(3)})
	require.NoError(t, err)
	require.Contains(t, string(out), "count = 3")
	require.NotContains(t, string(out), "count = 3.0")
}

// TestTOMLApplyFieldDatetime verifies that setting a TOML date-time keeps the
// surrounding document formatting and writes an unquoted datetime literal.
func TestTOMLApplyFieldDatetime(t *testing.T) {
	tm, err := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	require.NoError(t, err)

	src := []byte("# release info\ncreated = 2020-01-01T00:00:00Z\n")
	out, err := tomlApplyField(src, []dagql.String{"created"}, tm, map[string]any{"created": tm})
	require.NoError(t, err)
	require.Contains(t, string(out), "# release info")
	require.Contains(t, string(out), "created = 2024-01-02T03:04:05Z")

	// Inserting a new datetime key keeps existing content intact too.
	src = []byte("# config\ntitle = \"x\"\n")
	out, err = tomlApplyField(src, []dagql.String{"created"}, tm, map[string]any{"title": "x", "created": tm})
	require.NoError(t, err)
	require.Contains(t, string(out), "# config")
	require.Contains(t, string(out), `title = "x"`)
	require.Contains(t, string(out), "created = 2024-01-02T03:04:05Z")
}

// TestTOMLDataRoundTripTypes verifies that TOML types that the JSON value model
// cannot natively represent survive a source -> data -> value round-trip.
func TestTOMLDataRoundTripTypes(t *testing.T) {
	data, err := tomlSourceToData([]byte("created = 2024-01-02T03:04:05Z\nratio = inf\nneg = -inf\nn = 9007199254740993\n"))
	require.NoError(t, err)

	v, err := tomlDecodeData(data)
	require.NoError(t, err)
	m := v.(map[string]any)

	created, ok := m["created"].(time.Time)
	require.True(t, ok, "created is %T", m["created"])
	require.Equal(t, "2024-01-02T03:04:05Z", created.Format(time.RFC3339Nano))

	require.True(t, math.IsInf(m["ratio"].(float64), 1))
	require.True(t, math.IsInf(m["neg"].(float64), -1))
	require.Equal(t, int64(9007199254740993), m["n"])

	// And the value model re-encodes to proper TOML literals.
	out, err := tomlEncode(m)
	require.NoError(t, err)
	require.Contains(t, string(out), "created = 2024-01-02T03:04:05Z")
	require.Contains(t, string(out), "ratio = +inf")
	require.Contains(t, string(out), "n = 9007199254740993")
}

func TestTOMLEncodeLiteral(t *testing.T) {
	tm, err := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	require.NoError(t, err)

	for _, tc := range []struct {
		value any
		want  string
	}{
		{int64(5), "5"},
		{"hello", `"hello"`},
		{true, "true"},
		{2.5, "2.5"},
		{5.0, "5.0"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
		{math.NaN(), "nan"},
		{tm, "2024-01-02T03:04:05Z"},
		{[]any{int64(1), "two", true}, `[1, "two", true]`},
		{map[string]any{"b": int64(2), "a": "one"}, `{a = "one", b = 2}`},
	} {
		got, err := tomlEncodeLiteral(tc.value)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

// TestTOMLWrapperCollision ensures a genuine table using the wrapper sentinel
// key is not misinterpreted as a typed wrapper.
func TestTOMLWrapperCollision(t *testing.T) {
	src := []byte("[\"$dagger.toml\"]\nvalue = \"datetime\"\n")
	// Build the equivalent of what field() does: decode, navigate, re-marshal.
	data, err := tomlSourceToData(src)
	require.NoError(t, err)
	v, err := tomlDecodeData(data)
	require.NoError(t, err)
	m := v.(map[string]any)
	inner, ok := m["$dagger.toml"].(map[string]any)
	require.True(t, ok, "inner is %T", m["$dagger.toml"])
	require.Equal(t, "datetime", inner["value"])
}
