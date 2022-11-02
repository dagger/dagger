package test

import (
	"bytes"
	"encoding/json"
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/ts/templates"
	"github.com/stretchr/testify/require"
)

func TestArgs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"2 types": {`[ { "type": "string", "name": "ref" }, { "type": "string", "name": "tag" } ]`, "string ref, string tag"},
		"0 types": {`[]`, ""},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := template.New("args").Funcs(template.FuncMap{
				"subtract": templates.Subtract,
			})
			tmpl = template.Must(tmpl.ParseFiles("args.ts.tmpl", "arg.ts.tmpl"))

			jsonData := c.in

			type elem struct {
				Name string
				Type string
			}

			var elems []elem
			err := json.Unmarshal([]byte(jsonData), &elems)
			require.NoError(t, err)

			var b bytes.Buffer
			err = tmpl.ExecuteTemplate(&b, "args", elems)

			require.NoError(t, err)
			require.Equal(t, c.want, b.String())
		})
	}
}
