package dagql_test

import (
	"context"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/dagger/dagger/dagql"
)

// A parsed selector must not keep the query text alive: the lexer cuts names
// and unescaped literals out of the text, and a cached call keeps its
// selector's strings for as long as the call is cached.
func TestParseFieldDetachesQueryText(t *testing.T) {
	srv := newExternalDagqlServerForTest(t, Query{})
	dagql.Fields[Query]{
		dagql.Func("echo", func(ctx context.Context, self Query, args struct {
			Text  string
			Words dagql.ArrayInput[dagql.String]
		}) (string, error) {
			return args.Text, nil
		}),
	}.Install(srv)

	src := &ast.Source{Input: `{ echo(text: "/usr/local/bin/dispatch", words: ["engine-call", "es\"caped"]) }`}
	doc, err := parser.ParseQuery(src)
	require.NoError(t, err)
	field := doc.Operations[0].SelectionSet[0].(*ast.Field)

	inQuery := func(s string) bool {
		if s == "" {
			return false
		}
		start := uintptr(unsafe.Pointer(unsafe.StringData(src.Input)))
		at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
		return at >= start && at < start+uintptr(len(src.Input))
	}
	// The premise: the AST's strings share the query text's memory.
	require.True(t, inQuery(field.Name))
	require.True(t, inQuery(field.Arguments[0].Value.Raw))

	sel, _, err := srv.Root().ObjectType().ParseField(context.Background(), "", field, nil)
	require.NoError(t, err)

	require.Equal(t, "echo", sel.Field)
	require.False(t, inQuery(sel.Field), "field name")
	require.Len(t, sel.Args, 2)

	require.Equal(t, "text", sel.Args[0].Name)
	require.False(t, inQuery(sel.Args[0].Name), "argument name")
	text, ok := sel.Args[0].Value.(dagql.String)
	require.True(t, ok, "%T", sel.Args[0].Value)
	require.Equal(t, "/usr/local/bin/dispatch", text.String())
	require.False(t, inQuery(text.String()), "string argument")

	require.Equal(t, "words", sel.Args[1].Name)
	words, ok := sel.Args[1].Value.(dagql.ArrayInput[dagql.String])
	require.True(t, ok, "%T", sel.Args[1].Value)
	require.Len(t, words, 2)
	require.Equal(t, "engine-call", words[0].String())
	require.Equal(t, `es"caped`, words[1].String())
	for _, word := range words {
		require.False(t, inQuery(word.String()), "list element %q", word)
	}
}
