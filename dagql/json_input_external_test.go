package dagql_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// installJSONInputs installs fields that take a JSON argument and report
// what they were given: its JSON text, and the recipe ID of their own call.
func installJSONInputs(srv *dagql.Server) {
	srv.InstallScalar(core.JSON{})
	recipe := func(ctx context.Context) (string, error) {
		id, err := dagql.CurrentCall(ctx).RecipeID(ctx)
		if err != nil {
			return "", err
		}
		return id.Encode()
	}
	dagql.Fields[Query]{
		dagql.Func("jsonText", func(_ context.Context, _ Query, args struct {
			Value core.JSON
		}) (string, error) {
			return args.Value.String(), nil
		}),
		dagql.Func("optionalJSONText", func(_ context.Context, _ Query, args struct {
			Value dagql.Optional[core.JSON]
		}) (string, error) {
			if !args.Value.Valid {
				return "<null>", nil
			}
			return args.Value.Value.String(), nil
		}),
		dagql.Func("jsonListTexts", func(_ context.Context, _ Query, args struct {
			Values dagql.ArrayInput[core.JSON]
		}) ([]string, error) {
			texts := make([]string, len(args.Values))
			for i, v := range args.Values {
				texts[i] = v.String()
			}
			return texts, nil
		}),
		dagql.Func("jsonRecipe", func(ctx context.Context, _ Query, _ struct {
			Value core.JSON
		}) (string, error) {
			return recipe(ctx)
		}),
	}.Install(srv)
}

func TestJSONRawInputLiterals(t *testing.T) {
	srv := newExternalDagqlServerForTest(t, Query{})
	installJSONInputs(srv)
	gql := newTestClient(srv)

	for _, tc := range []struct {
		name    string
		literal string
		want    string
	}{
		{name: "object", literal: `{b: 1, a: "x"}`, want: `{"a":"x","b":1}`},
		{name: "empty object", literal: `{}`, want: `{}`},
		{name: "list", literal: `[1, "two", true, null]`, want: `[1,"two",true,null]`},
		{name: "empty list", literal: `[]`, want: `[]`},
		{name: "int", literal: `42`, want: `42`},
		{name: "float", literal: `1.5`, want: `1.5`},
		{name: "bool", literal: `true`, want: `true`},
		{
			name:    "nested",
			literal: `{list: [{deep: []}, [1, 2.5]], obj: {n: null, s: "<&>"}}`,
			want:    `{"list":[{"deep":[]},[1,2.5]],"obj":{"n":null,"s":"<&>"}}`,
		},
		{name: "string stays JSON text", literal: `"{\"b\": 1}"`, want: `{"b": 1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var res struct {
				JSONText         string
				OptionalJSONText string
			}
			req(t, gql, `query {
				jsonText(value: `+tc.literal+`)
				optionalJSONText(value: `+tc.literal+`)
			}`, &res)
			assert.Equal(t, res.JSONText, tc.want)
			assert.Equal(t, res.OptionalJSONText, tc.want)
		})
	}

	t.Run("null optional", func(t *testing.T) {
		var res struct{ OptionalJSONText string }
		req(t, gql, `query { optionalJSONText(value: null) }`, &res)
		assert.Equal(t, res.OptionalJSONText, "<null>")
	})

	t.Run("list of JSON", func(t *testing.T) {
		var res struct{ JSONListTexts []string }
		req(t, gql, `query { jsonListTexts(values: [{a: 1}, [true], "[\"text\"]"]) }`, &res)
		assert.DeepEqual(t, res.JSONListTexts, []string{`{"a":1}`, `[true]`, `["text"]`})
	})
}

func TestJSONRawInputVariables(t *testing.T) {
	srv := newExternalDagqlServerForTest(t, Query{})
	installJSONInputs(srv)
	gql := newTestClient(srv)

	for _, tc := range []struct {
		name string
		val  any
		want string
	}{
		{name: "object", val: map[string]any{"b": 1, "a": []any{"x", 2.5}}, want: `{"a":["x",2.5],"b":1}`},
		{name: "list", val: []any{map[string]any{}, []any{}, nil}, want: `[{},[],null]`},
		{name: "big int", val: uint64(12345678901234567890), want: `12345678901234567890`},
		{name: "bool", val: false, want: `false`},
		{name: "string stays JSON text", val: `{"b": 1}`, want: `{"b": 1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var res struct{ JSONText string }
			reqVars(t, gql, `query($v: JSON!) { jsonText(value: $v) }`, &res, client.Var("v", tc.val))
			assert.Equal(t, res.JSONText, tc.want)
		})
	}

	t.Run("variable inside a literal", func(t *testing.T) {
		var res struct{ JSONText string }
		reqVars(t, gql, `query($n: Int!, $s: String!) { jsonText(value: {n: $n, s: [$s]}) }`, &res,
			client.Var("n", 7), client.Var("s", "seven"))
		assert.Equal(t, res.JSONText, `{"n":7,"s":["seven"]}`)
	})
}

func reqVars(t *testing.T, gql *client.Client, query string, res any, opts ...client.Option) {
	t.Helper()
	assert.NilError(t, gql.Post(query, res, opts...))
}

// A raw JSON input is stored as its compact JSON text, so its call has the
// same ID and cache key as the equivalent string input.
func TestJSONRawInputMatchesStringInputID(t *testing.T) {
	srv := newExternalDagqlServerForTest(t, Query{})
	installJSONInputs(srv)
	gql := newTestClient(srv)

	var fromLiteral, fromVariable, fromString, fromOther struct{ JSONRecipe string }
	req(t, gql, `query { jsonRecipe(value: {b: [1, true], a: {c: null}}) }`, &fromLiteral)
	reqVars(t, gql, `query($v: JSON!) { jsonRecipe(value: $v) }`, &fromVariable,
		client.Var("v", map[string]any{"a": map[string]any{"c": nil}, "b": []any{1, true}}))
	req(t, gql, `query { jsonRecipe(value: "{\"a\":{\"c\":null},\"b\":[1,true]}") }`, &fromString)
	req(t, gql, `query { jsonRecipe(value: "{\"a\":{\"c\":null},\"b\":[1,false]}") }`, &fromOther)

	assert.Assert(t, fromLiteral.JSONRecipe != "")
	eqIDs(t, fromLiteral.JSONRecipe, fromString.JSONRecipe)
	eqIDs(t, fromVariable.JSONRecipe, fromString.JSONRecipe)
	assert.Assert(t, fromOther.JSONRecipe != fromString.JSONRecipe)
}
