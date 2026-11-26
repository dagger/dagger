package dotenv

import (
	"slices"
	"strings"
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
			name: "export prefix",
			environ: []string{
				"export FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "export prefix with quoted value",
			environ: []string{
				`export FOO="bar baz"`,
			},
			want: map[string]string{
				"FOO": "bar baz",
			},
		},
		{
			name: "export prefix with expansion",
			environ: []string{
				"export FOO=bar",
				"export BAZ=$FOO-baz",
			},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "bar-baz",
			},
		},
		{
			name: "export prefix with extra whitespace",
			environ: []string{
				"export   FOO=bar",
			},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name: "key named export-like is not stripped",
			environ: []string{
				"exportable=yes",
			},
			want: map[string]string{
				"exportable": "yes",
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
			got, err := All(tt.environ, nil, true)
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

func TestAllWithContext(t *testing.T) {
	own := []string{
		`SOURCE=${ROOT_DIR}/pepe`,
	}
	context := []string{
		`ROOT_DIR=../..`,
		`HELM_SOURCE=${UNDEFINED_THING}`,
	}

	all, err := AllWithContext(own, context, nil, true)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"SOURCE": "../../pepe"}, all)

	val, ok, err := LookupWithContext(own, context, "SOURCE", nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "../../pepe", val)

	_, ok, err = LookupWithContext(own, context, "ROOT_DIR", nil)
	require.NoError(t, err)
	require.False(t, ok, "context-only variable should not be found")
}

func TestNoSystemLookupWhenLiteral(t *testing.T) {
	var called bool
	lookup := func(name string) string {
		called = true
		return ""
	}
	environ := []string{
		"FOO=bar",
	}
	val, ok, err := Lookup(environ, "FOO", lookup)
	require.NoError(t, err)
	require.True(t, ok, "Lookup should find bar")
	require.Equal(t, "bar", val)
	require.False(t, called, "Lookup should not be called")
}

func TestReferencedContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		own     []string
		context []string
		kept    []string
		// prefix, when set, is also stripped from own's names, as namespacing
		// a module's defaults for one of its functions does, and own expands
		// the same that way too.
		prefix string
		// built: a name expansion builds from parts of values reads only the
		// lines kept, so own expands differently.
		built bool
		// malformed: all of context doesn't parse, while what's kept does.
		malformed bool
		// fails: own doesn't expand, with all of context or what's kept.
		fails bool
	}{
		{
			name:    "only what a value names",
			own:     []string{"SOURCE=${ROOT_DIR}/pepe"},
			context: []string{"ROOT_DIR=../..", "HELM_SOURCE=${UNDEFINED_THING}", "UNRELATED_TOKEN=x"},
			kept:    []string{"ROOT_DIR=../.."},
		},
		{
			name:    "through another kept line",
			own:     []string{"A=${B}"},
			context: []string{"B=${C}-b", "C=c", "D=d"},
			kept:    []string{"B=${C}-b", "C=c"},
		},
		{
			name:    "every branch",
			own:     []string{"A=$B/x", "E=${UNSET:-$C}", "F=${G:+$H}"},
			context: []string{"B=b", "C=c", "H=h", "D=d"},
			kept:    []string{"B=b", "C=c", "H=h"},
		},
		{
			name:    "IFS splits what a value expands",
			own:     []string{"A=$B"},
			context: []string{"B=a:b", "IFS=:", "D=d"},
			kept:    []string{"B=a:b", "IFS=:"},
		},
		{
			name:    "a tilde reads HOME",
			own:     []string{"A=~/x"},
			context: []string{"HOME=/home/someone", "D=d"},
			kept:    []string{"HOME=/home/someone"},
		},
		{
			name:    "arithmetic reads its bare names",
			own:     []string{"N=$((B + 1))"},
			context: []string{"B=2", "D=d"},
			kept:    []string{"B=2"},
		},
		{
			name:    "arithmetic follows a name a value holds",
			own:     []string{"N=$((B + 1))"},
			context: []string{"B=C", "C=2", "D=d"},
			kept:    []string{"B=C", "C=2"},
		},
		{
			name:    "constant arithmetic reads nothing",
			own:     []string{"N=$((1 + 1))"},
			context: []string{"D=d"},
		},
		{
			name:    "a slice reads its variable and its bounds",
			own:     []string{"A=${B:O:2}"},
			context: []string{"B=bee", "O=1", "D=d"},
			kept:    []string{"B=bee", "O=1"},
		},
		{
			name:    "an indirection reads the target a value names",
			own:     []string{"A=${!P}"},
			context: []string{"P=SECRET", "SECRET=value", "D=d"},
			kept:    []string{"P=SECRET", "SECRET=value"},
		},
		{
			name:    "an indirection reads a target written in quotes",
			own:     []string{"A=${!P}", "B=${!Q}"},
			context: []string{"P='SECRET'", "Q=\"OTHER\"", "SECRET=value", "OTHER=other", "UNRELATED_PASSWORD=private"},
			kept:    []string{"P='SECRET'", "Q=\"OTHER\"", "SECRET=value", "OTHER=other"},
		},
		{
			name:    "an indirection reads a target written in a branch",
			own:     []string{"A=${!P}"},
			context: []string{"P=${UNSET:-SECRET}", "SECRET=value", "UNRELATED_PASSWORD=private"},
			kept:    []string{"P=${UNSET:-SECRET}", "SECRET=value"},
		},
		{
			name:    "a word that is a line's name keeps it",
			own:     []string{"MSG=hello world"},
			context: []string{"world=w", "D=d"},
			kept:    []string{"world=w"},
		},
		{
			name:    "an indirection after namespacing",
			own:     []string{"HELLO_GREETING=${!P}", "HELLO_P=TARGET"},
			context: []string{"P=OTHER", "OTHER=old", "TARGET=desired", "UNRELATED_PASSWORD=private"},
			kept:    []string{"P=OTHER", "OTHER=old", "TARGET=desired"},
			prefix:  "HELLO_",
		},
		{
			name:    "a name built from parts reads only what's kept",
			own:     []string{"A=${!P}"},
			context: []string{"P=${LEFT}${RIGHT}", "LEFT=SE", "RIGHT=CRET", "SECRET=value", "D=d"},
			kept:    []string{"P=${LEFT}${RIGHT}", "LEFT=SE", "RIGHT=CRET"},
			built:   true,
		},
		{
			name:    "own wins",
			own:     []string{"A=${B}", "B=own"},
			context: []string{"B=ctx"},
		},
		{
			name:    "a name in single quotes is kept, though not read",
			own:     []string{"A='${B}'"},
			context: []string{"B=b", "D=d"},
			kept:    []string{"B=b"},
		},
		{
			name:    "duplicates",
			own:     []string{"A=${B}"},
			context: []string{"B=b", "D=d", "B=b", "D=d"},
			kept:    []string{"B=b", "B=b"},
		},
		{
			name:      "a malformed line nothing names",
			own:       []string{"A=${B}"},
			context:   []string{"B=b", "D=$(("},
			kept:      []string{"B=b"},
			malformed: true,
		},
		{
			name:    "a malformed line a value names",
			own:     []string{"A=${D}"},
			context: []string{"B=b", "D=$(("},
			kept:    []string{"D=$(("},
			fails:   true,
		},
		{
			name:    "own that doesn't parse",
			own:     []string{"A=$(("},
			context: []string{"B=b"},
			fails:   true,
		},
		{
			name:    "nothing named",
			own:     []string{"A=plain"},
			context: []string{"B=b"},
		},
		{
			name: "no context",
			own:  []string{"A=${B}"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kept := ReferencedContext(tc.own, tc.context)
			require.Equal(t, tc.kept, kept)

			// What it leaves out changes no value own expands to.
			host := func(name string) string { return "host-" + name }
			expand := func(own, context []string) {
				all, allErr := AllWithContext(own, context, host, false)
				got, gotErr := AllWithContext(own, kept, host, false)
				switch {
				case tc.fails:
					require.Error(t, allErr)
					require.Error(t, gotErr)
				case tc.malformed:
					require.Error(t, allErr)
					require.NoError(t, gotErr)
				case tc.built:
					require.NoError(t, allErr)
					require.NoError(t, gotErr)
					require.NotEqual(t, all, got)
				default:
					require.NoError(t, allErr)
					require.NoError(t, gotErr)
					require.Equal(t, all, got)
				}
			}
			expand(tc.own, tc.context)
			if tc.prefix != "" {
				// Namespacing moves the lines without the prefix into context,
				// after the caller's lines, and strips it from the others.
				var namespaced, moved []string
				for _, line := range tc.own {
					if after, ok := strings.CutPrefix(line, tc.prefix); ok {
						namespaced = append(namespaced, after)
					} else {
						moved = append(moved, line)
					}
				}
				all, allErr := AllWithContext(namespaced, append(slices.Clone(tc.context), moved...), host, false)
				got, gotErr := AllWithContext(namespaced, append(slices.Clone(kept), moved...), host, false)
				require.NoError(t, allErr)
				require.NoError(t, gotErr)
				require.Equal(t, all, got)
			}
		})
	}
}
