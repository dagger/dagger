package test

import (
	"bytes"
	"fmt"
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/ts/templates"
	"github.com/stretchr/testify/require"
)

func templateHelper(templateType string) *template.Template {
	tmpl := template.Must(template.New(templateType).Funcs(templates.FuncMap).ParseFiles(fmt.Sprintf("%s.ts.tmpl", templateType)))
	return tmpl
}
func TestComment(t *testing.T) {
	templateType := "comment"
	tmpl := templateHelper(templateType)
	want := `/**
 * This is a comment
 */`
	comments := []string{"This is a comment"}

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, templateType, comments)
	require.NoError(t, err)

	require.Equal(t, want, b.String())
}
