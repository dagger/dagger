package test

import (
	"bytes"
	"encoding/json"
	"testing"

	generator "github.com/dagger/dagger/codegen/generator/go"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestObject(t *testing.T) {
	tmpl := templateHelper(t, "object", "comment", "field", "input_args", "arg")

	object := objectInit(t, containerExecArgsJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)

	want := expectedFunc

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

var expectedFunc = `
/**
 * 
 */
class Container extends BaseApi {

  get getQueryTree() {
    return this._queryTree;
  }

  exec(args: ContainerExecArgs)
}
`

func objectInit(t *testing.T, jsonString string) *introspection.Type {
	t.Helper()
	var object introspection.Type
	err := json.Unmarshal([]byte(jsonString), &object)
	require.NoError(t, err)

	schema := introspection.Schema{
		Types: []*introspection.Type{
			&object,
		},
	}

	generator.SetSchemaParents(&schema)
	return &object
}
