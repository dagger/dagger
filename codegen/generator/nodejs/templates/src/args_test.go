package test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/codegen/generator/nodejs/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestArgs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"2 types":    {`[ { "name": "ref", "type": { "kind": "NON_NULL", "ofType": {"name": "string", "kind": "SCALAR" } } }, { "name": "tag", "type": { "name": "string", "kind": "SCALAR" } } ]`, "ref: string, tag?: string"},
		"0 types":    {`[]`, ""},
		"1 non null": {fromArgs, "address: string"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templates.New()

			jsonData := c.in

			var elems []introspection.Field
			err := json.Unmarshal([]byte(jsonData), &elems)
			require.NoError(t, err)

			for _, elem := range elems {
				t.Log("optional:", elem.Name, ":", elem.TypeRef.IsOptional())
			}

			var b bytes.Buffer
			err = tmpl.ExecuteTemplate(&b, "args", elems)

			require.NoError(t, err)
			require.Equal(t, c.want, b.String())
		})
	}
}

const fromArgs = `
          [
            {
              "defaultValue": null,
              "description": "",
              "name": "address",
              "type": {
                "kind": "NON_NULL",
                "ofType": {
                  "kind": "SCALAR",
                  "name": "String"
                }
              }
            }
          ]`
