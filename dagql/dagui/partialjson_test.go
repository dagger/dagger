package dagui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodePartialJSON(t *testing.T) {
	for _, tc := range []struct {
		name      string
		doc       string
		want      map[string]any
		complete  bool
		truncated []string
	}{
		{name: "empty", doc: ""},
		{name: "space", doc: "  \n"},
		{name: "not an object", doc: `["a"]`},
		{name: "not an object, partial", doc: `["a`},
		{name: "garbage", doc: `hello`},
		{name: "null", doc: `null`},
		{name: "open brace", doc: `{`, want: map[string]any{}},
		{name: "partial key", doc: `{"sou`, want: map[string]any{}},
		{name: "dangling key", doc: `{"source"`, want: map[string]any{}},
		{name: "dangling colon", doc: `{"source": `, want: map[string]any{}},
		{
			name: "partial string", doc: `{"source":"let x = `,
			want: map[string]any{"source": "let x = "}, truncated: []string{"source"},
		},
		{
			name: "dangling comma", doc: `{"at":"notes", `,
			want: map[string]any{"at": "notes"},
		},
		{
			name: "dangling second key", doc: `{"at":"notes","sou`,
			want: map[string]any{"at": "notes"},
		},
		{
			name: "complete", doc: `{"at":"notes","source":"1"}` + "\n",
			want: map[string]any{"at": "notes", "source": "1"}, complete: true,
		},
		{
			name: "trailing garbage", doc: `{"source":"1"}` + "\nRESULT\n",
			want: map[string]any{"source": "1"}, complete: true,
		},
		{
			name: "two documents", doc: `{"source":"1"}{"source":"2"}`,
			want: map[string]any{"source": "1"}, complete: true,
		},

		// Escapes.
		{
			name: "escapes", doc: `{"source":"say(\"hi\")\n\tdone \\ \u00e9`,
			want: map[string]any{"source": "say(\"hi\")\n\tdone \\ é"}, truncated: []string{"source"},
		},
		{
			name: "trailing backslash", doc: `{"source":"say(\`,
			want: map[string]any{"source": "say("}, truncated: []string{"source"},
		},
		{
			name: "escaped backslash at end", doc: `{"source":"a\\`,
			want: map[string]any{"source": `a\`}, truncated: []string{"source"},
		},
		{
			name: "escaped quote at end", doc: `{"source":"a\"`,
			want: map[string]any{"source": `a"`}, truncated: []string{"source"},
		},
		{
			name: "partial unicode escape", doc: `{"source":"caf\u00`,
			want: map[string]any{"source": "caf"}, truncated: []string{"source"},
		},
		{
			name: "bare unicode escape", doc: `{"source":"caf\u`,
			want: map[string]any{"source": "caf"}, truncated: []string{"source"},
		},
		{
			name: "half a surrogate pair", doc: `{"source":"party \ud83c`,
			want: map[string]any{"source": "party "}, truncated: []string{"source"},
		},
		{
			name: "whole surrogate pair", doc: `{"source":"party \ud83c\udf89`,
			want: map[string]any{"source": "party 🎉"}, truncated: []string{"source"},
		},
		{
			name: "split UTF-8", doc: `{"source":"日本` + "\xe8\xaa",
			want: map[string]any{"source": "日本"}, truncated: []string{"source"},
		},
		{
			// a cut-off stream ended with a newline, as displayPhases.Close
			// ends every stream
			name: "raw newline after a cut-off string", doc: `{"source":"let x = 1` + "\n",
			want: map[string]any{"source": "let x = 1"}, truncated: []string{"source"},
		},
		{
			name: "string that won't decode", doc: `{"at":"notes","source":"bad \x`,
			want: map[string]any{"at": "notes"},
		},

		// Scalars that may still grow are left out until they're done.
		{name: "partial number", doc: `{"offset":12`, want: map[string]any{}},
		{
			name: "number done", doc: `{"offset":12,`,
			want: map[string]any{"offset": float64(12)},
		},
		{name: "partial literal", doc: `{"all":tr`, want: map[string]any{}},
		{
			name: "literal done", doc: `{"all":true }`,
			want: map[string]any{"all": true}, complete: true,
		},

		// Nesting.
		{
			name: "Timeout streaming its call",
			doc:  `{"duration":"2m","tool":"DangEval","arguments":{"source":"let x = `,
			want: map[string]any{
				"duration": "2m", "tool": "DangEval",
				"arguments": map[string]any{"source": "let x = "},
			},
			truncated: []string{"arguments", "source"},
		},
		{
			name: "nested object opening", doc: `{"tool":"DangEval","arguments":{`,
			want: map[string]any{"tool": "DangEval", "arguments": map[string]any{}},
		},
		{
			name: "nested dangling key", doc: `{"tool":"DangEval","arguments":{"sou`,
			want: map[string]any{"tool": "DangEval", "arguments": map[string]any{}},
		},
		{
			name: "array", doc: `{"args":["go","test",`,
			want: map[string]any{"args": []any{"go", "test"}},
		},
		{
			name: "array partial element", doc: `{"args":["go","te`,
			want: map[string]any{"args": []any{"go", "te"}}, truncated: []string{"args", "1"},
		},
		{
			name: "array of objects", doc: `{"edits":[{"old":"a","new":"b"},{"old":"c`,
			want: map[string]any{"edits": []any{
				map[string]any{"old": "a", "new": "b"},
				map[string]any{"old": "c"},
			}},
			truncated: []string{"edits", "1", "old"},
		},
		{
			name: "empty array", doc: `{"args":[],"x":[`,
			want: map[string]any{"args": []any{}, "x": []any{}},
		},
		{
			name: "closed nested, open outer", doc: `{"arguments":{"source":"1"}`,
			want: map[string]any{"arguments": map[string]any{"source": "1"}},
		},
		{
			name: "whitespace everywhere", doc: "{ \"a\" : [ 1 , \"b\" ] , \"c\" : \"d",
			want: map[string]any{"a": []any{float64(1), "b"}, "c": "d"}, truncated: []string{"c"},
		},
		{
			name: "invalid after valid", doc: `{"at":"notes",}`,
			want: map[string]any{"at": "notes"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodePartialJSON(tc.doc)
			assert.Equal(t, tc.want, got.Value)
			assert.Equal(t, tc.complete, got.Complete)
			assert.Equal(t, tc.truncated, got.Truncated)
		})
	}
}

// TestDecodePartialJSONEveryPrefix streams documents a byte at a time: every
// prefix decodes, never to a value that isn't (a prefix of) the whole, and
// the whole decodes whole.
func TestDecodePartialJSONEveryPrefix(t *testing.T) {
	for _, doc := range []string{
		`{"duration":"2m","tool":"DangEval","arguments":{"at":"notes","source":"let greeting = shout(\"hi\")\n\tgreeting + \"!\" # \u00e9 \ud83c\udf89 \\ done"}}`,
		`{ "args" : [ "go", "test", "-run", "TestX" ], "limit": 20, "all": false, "none": null, "nested": [[1, 2], {"k": [true]}] }`,
		`{"source":"日本語 🎉 \"quoted\""}`,
	} {
		whole := decodePartialJSON(doc)
		require.True(t, whole.Complete, doc)
		for n := range len(doc) {
			got := decodePartialJSON(doc[:n])
			assert.False(t, got.Complete, "%q", doc[:n])
			if got.Value == nil {
				assert.Empty(t, strings.TrimSpace(doc[:n]), "%q decoded to nothing", doc[:n])
				continue
			}
			assertPrefixValue(t, whole.Value, got.Value, doc[:n])
		}
	}
}

// assertPrefixValue asserts that got could be what a prefix of a document
// decoding to want holds: each of its members is in want, its strings are
// prefixes, its lists are prefixes.
func assertPrefixValue(t *testing.T, want, got any, doc string) {
	t.Helper()
	switch got := got.(type) {
	case map[string]any:
		want, ok := want.(map[string]any)
		require.True(t, ok, "%q: object where %T", doc, want)
		for k, v := range got {
			require.Contains(t, want, k, "%q", doc)
			assertPrefixValue(t, want[k], v, doc)
		}
	case []any:
		want, ok := want.([]any)
		require.True(t, ok, "%q: array where %T", doc, want)
		require.LessOrEqual(t, len(got), len(want), "%q", doc)
		for i, v := range got {
			assertPrefixValue(t, want[i], v, doc)
		}
	case string:
		want, ok := want.(string)
		require.True(t, ok, "%q: string where %T", doc, want)
		require.True(t, strings.HasPrefix(want, got), "%q: %q is not a prefix of %q", doc, got, want)
	default:
		require.Equal(t, want, got, "%q", doc)
	}
}
