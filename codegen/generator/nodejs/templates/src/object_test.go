package test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestObject(t *testing.T) {
	tmpl := templateHelper(t)

	object := objectInit(t, containerExecArgsJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)

	want := wantTestObject

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

var wantTestObject = `
class Container extends BaseClient {
  exec(args?: ContainerExecArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ], port: this.port})
  }
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

func objectsInit(t *testing.T, jsonString string) introspection.Types {
	t.Helper()
	var objects introspection.Types
	err := json.Unmarshal([]byte(jsonString), &objects)
	require.NoError(t, err)

	schema := introspection.Schema{
		Types: objects,
	}

	generator.SetSchemaParents(&schema)
	return objects
}
