package test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

func join(sep string, str ...string) string {
	return strings.Join(str, sep)
}

// TODO move to non test package
func subtract(a, b int) int {
	return a - b
}

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
				"subtract": subtract,
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
