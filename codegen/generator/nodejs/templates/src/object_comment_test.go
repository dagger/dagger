package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComment(t *testing.T) {
	templateType := "object_comment"
	t.Run("simple comment", func(t *testing.T) {
		tmpl := templateHelper(t)
		want := `/**
 * This is a comment
 */`
		comments := "This is a comment"

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, comments)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
	t.Run("multi line comment", func(t *testing.T) {
		tmpl := templateHelper(t)
		want := `/**
 * This is a comment
 * that spans on multiple lines
 */`
		comments := "This is a comment\nthat spans on multiple lines"

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, comments)
		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})
}
