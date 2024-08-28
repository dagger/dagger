package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePragmaComment(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		expected map[string]string
		rest     string
	}{
		{
			name:    "single key",
			comment: "+foo",
			expected: map[string]string{
				"foo": "",
			},
			rest: "",
		},
		{
			name:    "single key with trailing lf",
			comment: "+foo\n",
			expected: map[string]string{
				"foo": "",
			},
			rest: "",
		},
		{
			name:    "single key with trailing crlf",
			comment: "+foo\r\n",
			expected: map[string]string{
				"foo": "",
			},
			rest: "",
		},
		{
			name:    "single key with leading whitespace",
			comment: " \t +foo",
			expected: map[string]string{
				"foo": "",
			},
			rest: "",
		},
		{
			name:    "single key-value",
			comment: "+foo=bar",
			expected: map[string]string{
				"foo": "bar",
			},
			rest: "",
		},
		{
			name:    "single key-value with trailing",
			comment: "+foo=bar\n",
			expected: map[string]string{
				"foo": "bar",
			},
			rest: "",
		},
		{
			name:    "multiple key-value",
			comment: "+foo=bar\n+baz=qux",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "",
		},
		{
			name:    "interpolated key-value",
			comment: "line 1\n+foo=bar\nline 2\n+baz=qux\nline 3",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "line 1\nline 2\nline 3",
		},
		{
			name:    "interpolated key-value with trailing",
			comment: "line 1\n+foo=bar\nline 2\n+baz=qux\nline 3\n",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "line 1\nline 2\nline 3\n",
		},
		{
			name:    "interpolated key-value with crlf",
			comment: "line 1\r\n+foo=bar\r\nline 2\r\n+baz=qux\r\nline 3",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "line 1\r\nline 2\r\nline 3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, rest := parsePragmaComment(test.comment)
			require.Equal(t, test.expected, actual)
			require.Equal(t, test.rest, rest)
		})
	}
}
