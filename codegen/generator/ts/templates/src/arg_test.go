package test

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

func TestArg(t *testing.T) {
	tmpl := template.Must(template.New("arg1").ParseFiles("arg.ts.tmpl"))

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "arg", struct {
		Name string
		Type string
	}{
		Type: "string",
		Name: "ref",
	})

	want := "string ref"

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}
