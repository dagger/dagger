package test

import (
	"bytes"
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
	tmpl := template.New("args").Funcs(template.FuncMap{
		"join":     join,
		"subtract": subtract,
	})
	tmpl = template.Must(tmpl.ParseFiles("args.ts.tmpl", "arg.ts.tmpl"))

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "args", []struct {
		Name string
		Type string
	}{
		{
			Type: "string",
			Name: "ref",
		},
		{
			Type: "string",
			Name: "tag",
		},
	})

	want := "string ref, string tag"

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}
