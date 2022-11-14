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
		"2 types": {`[ { "name": "ref", "type": { "name": "string", "kind": "SCALAR" } }, { "name": "tag", "type": { "name": "string", "kind": "SCALAR" } } ]`, "string ref, string tag"},
		"0 types": {`[]`, ""},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templates.New()

			jsonData := c.in

			var elems []introspection.Field
			err := json.Unmarshal([]byte(jsonData), &elems)
			require.NoError(t, err)

			var b bytes.Buffer
			err = tmpl.ExecuteTemplate(&b, "args", elems)

			require.NoError(t, err)
			require.Equal(t, c.want, b.String())
		})
	}
}
