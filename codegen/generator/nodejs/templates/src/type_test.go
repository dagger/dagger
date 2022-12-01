package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestType(t *testing.T) {
	tmpl := templateHelper(t)

	object := objectInit(t, fieldArgsTypeJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "type", object)

	want := expectedFieldArgsType

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

var expectedFieldArgsType = `
/**
 * Hola
 */
export type Container = string;
`

var fieldArgsTypeJSON = `
      {
        "kind": "SCALAR",
        "name": "Container",
        "description": "Hola"
    }
`
