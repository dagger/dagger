package dotenv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllRaw(t *testing.T) {
	input := []string{
		"FOO=bar",
		"EMPTY=",
	}
	want := map[string]string{
		"FOO":   "bar",
		"EMPTY": "",
	}
	got := AllRaw(input)
	require.Equal(t, want, got)
}

func TestAllExpansion(t *testing.T) {
	tests := []struct {
		name      string
		environ   []string
		want      map[string]string
		wantError bool
	}{
		{
			name: "basic assignment",
			environ: []string{
				"FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "quoted string",
			environ: []string{
				`FOO="a b c"`,
			},
			want: map[string]string{
				"FOO": "a b c",
			},
		},
		{
			name: "cycle detection",
			environ: []string{
				"A=$B",
				"B=$C",
				"C=$A",
			},
			wantError: true,
		},
		{
			name: "unquoted multiple words collapse to one",
			environ: []string{
				"FOO=a b c",
			},
			want: map[string]string{
				"FOO": "a b c", // dotenv semantics
			},
		},
		{
			name: "unquoted multiple words, multiple spacs",
			environ: []string{
				"FOO=a   b c",
			},
			want: map[string]string{
				"FOO": "a   b c",
			},
		},
		{
			name: "variable expansion",
			environ: []string{
				"FOO=bar",
				"BAZ=$FOO-baz",
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "bar-baz",
			},
		},
		{
			name: "error on missing variable",
			environ: []string{
				"QUX=$MISSING",
			},
			wantError: true,
		},
		{
			name: "order doesn't matter",
			environ: []string{
				"BAZ=$FOO-baz",
				"FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "bar-baz",
			},
		},
		{
			name: "expansion with default value",
			environ: []string{
				"FOO=bar",
				"BAZ=${MISSING:-default}",
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "default",
			},
		},
		{
			name: "expansion with default value when variable exists",
			environ: []string{
				"FOO=bar",
				"EXISTING=value",
				"BAZ=${EXISTING:-default}",
			},
			want: map[string]string{
				"FOO":      "bar",
				"EXISTING": "value",
				"BAZ":      "value",
			},
		},
		{
			name: "quoted string with expansion",
			environ: []string{
				`FOO=bar`,
				`BAZ="prefix $FOO suffix"`,
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "prefix bar suffix",
			},
		},
		{
			name: "quoted string with 2 levels of expansion",
			environ: []string{
				`animal="dog"`,
				`message="hello, nice ${animal}"`,
				`story="once upon a time, there was a man who said $message"`,
			},
			want: map[string]string{
				"animal":  "dog",
				"message": "hello, nice dog",
				"story":   "once upon a time, there was a man who said hello, nice dog",
			},
		},
		{
			name: "single quoted assignment (no expansion)",
			environ: []string{
				`animal=dog`,
				`single_quoted_var='hello, nice $animal'`,
			},
			want: map[string]string{
				"animal":            "dog",
				"single_quoted_var": "hello, nice $animal", // single quotes prevent expansion
			},
		},
		{
			name: "single quoted variable (no expansion)",
			environ: []string{
				`animal=dog`,
				`single_quoted_var=hello, nice '$animal'`,
			},
			want: map[string]string{
				"animal":            "dog",
				"single_quoted_var": "hello, nice $animal", // single quotes prevent expansion
			},
		},
		{
			name: "escaped spaces",
			environ: []string{
				`FOO=a\ b\ c`,
			},
			want: map[string]string{
				"FOO": "a b c",
			},
		},
		{
			name: "empty line",
			environ: []string{
				"",
				"FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "whitespace only line",
			environ: []string{
				"   ",
				"FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "empty assignment",
			environ: []string{
				"EMPTY=",
			},
			want: map[string]string{
				"EMPTY": "",
			},
		},
		{
			name: "can't self reference",
			environ: []string{
				"FOO=bar",
				"FOO=$FOO-suffix",
			},
			wantError: true,
		},
		{
			name: "braced variable expansion",
			environ: []string{
				"FOO=bar",
				"BAZ=${FOO}baz",
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "barbaz",
			},
		},
		{
			name: "mixed quotes and expansion",
			environ: []string{
				"FOO=bar",
				`BAZ='$FOO'`,
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "$FOO", // single quotes prevent expansion
			},
		},
		{
			name: "special characters in value",
			environ: []string{
				"SPECIAL=!@#$%^&*()",
			},
			want: map[string]string{
				"SPECIAL": "!@#$%^&*()",
			},
		},
		{
			name: "JSON array must be protected with quotes",
			environ: []string{
				`no_quotes=["ga", "bu", "zo", "meu", 42]`,
				`single_quotes='["ga", "bu", "zo", "meu", 42]'`,
				`double_quotes="[\"ga\", \"bu\", \"zo\", \"meu\", 42]"`,
			},
			want: map[string]string{
				`no_quotes`:     `[ga, bu, zo, meu, 42]`,
				`single_quotes`: `["ga", "bu", "zo", "meu", 42]`,
				`double_quotes`: `["ga", "bu", "zo", "meu", 42]`,
			},
		},
		{
			name: "JSON object must be protected with quotes",
			environ: []string{
				`no_quotes={"name": "John Wick", "age": 58, "occupation": "assassin"}`,
				`single_quotes='{"name": "John Wick", "age": 58, "occupation": "assassin"}'`,
				`double_quotes="{\"name\": \"John Wick\", \"age\": 58, \"occupation\": \"assassin\"}"`,
			},
			want: map[string]string{
				`no_quotes`:     `{name: John Wick, age: 58, occupation: assassin}`,
				`single_quotes`: `{"name": "John Wick", "age": 58, "occupation": "assassin"}`,
				`double_quotes`: `{"name": "John Wick", "age": 58, "occupation": "assassin"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := All(tt.environ, nil)
			if tt.wantError {
				require.Error(t, err, tt.name)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got, tt.name)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	environ := []string{
		"FOO=bar",
		"BAZ=$FOO-baz",
	}
	val, ok, err := Lookup(environ, "BAZ", nil)
	require.NoError(t, err)
	require.True(t, ok, "Lookup should find BAZ")
	require.Equal(t, "bar-baz", val)
}
