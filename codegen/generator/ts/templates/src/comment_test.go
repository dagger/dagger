package test

import (
	"bytes"
	"fmt"
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/ts/templates"
	"github.com/stretchr/testify/require"
)

func templateHelper(t *testing.T, templateType string, templateDeps ...string) *template.Template {
	t.Helper()
	files := []string{fmt.Sprintf("%s.ts.tmpl", templateType)}
	for _, tmpl := range templateDeps {
		files = append(files, fmt.Sprintf("%s.ts.tmpl", tmpl))
	}

	tmpl := template.Must(template.New(templateType).Funcs(templates.FuncMap).ParseFiles(files...))
	return tmpl
}
func TestComment(t *testing.T) {
	t.Run("simple comment", func(t *testing.T) {
		templateType := "comment"
		tmpl := templateHelper(t, templateType)
		want := `/**
 * This is a comment
 */`
		comments := []string{"This is a comment"}

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, comments)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
	t.Run("multi line comment", func(t *testing.T) {
		templateType := "comment"
		tmpl := templateHelper(t, templateType)
		want := `/**
 * This is a comment
 * that spans on multiple lines
 */`
		comments := []string{"This is a comment", "that spans on multiple lines"}

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, comments)
		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})
}
