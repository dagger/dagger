package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestType(t *testing.T) {
	t.Run("scalar", func(t *testing.T) {
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
		tmpl := templateHelper(t)

		object := objectInit(t, fieldArgsTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := expectedFieldArgsType

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("args", func(t *testing.T) {
		var expectedFieldArgsType = `
export type ContainerExecOpts = {

  /**
   * Command to run instead of the container's default command
   */
  args?: string[];

  /**
   * Content to write to the command's standard input before closing
   */
  stdin?: string;

  /**
   * Redirect the command's standard output to a file in the container
   */
  redirectStdout?: string;

  /**
   * Redirect the command's standard error to a file in the container
   */
  redirectStderr?: string;

  /**
   * Provide dagger access to the executed command
   * Do not use this option unless you trust the command being executed
   * The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
   */
  experimentalPrivilegedNesting?: boolean;
};
`

		var fieldArgsTypeJSON = `
    {
      "description": "An OCI-compatible container, also known as a docker container",
      "fields": [
	{
          "args": [
            {
              "defaultValue": null,
              "description": "Command to run instead of the container's default command",
              "name": "args",
              "type": {
                "kind": "LIST",
                "ofType": {
                  "kind": "NON_NULL",
                  "ofType": {
                    "kind": "SCALAR",
                    "name": "String"
                  }
                }
              }
            },
            {
              "defaultValue": null,
              "description": "Content to write to the command's standard input before closing",
              "name": "stdin",
              "type": {
                "kind": "SCALAR",
                "name": "String"
              }
            },
            {
              "defaultValue": null,
              "description": "Redirect the command's standard output to a file in the container",
              "name": "redirectStdout",
              "type": {
                "kind": "SCALAR",
                "name": "String"
              }
            },
            {
              "defaultValue": null,
              "description": "Redirect the command's standard error to a file in the container",
              "name": "redirectStderr",
              "type": {
                "kind": "SCALAR",
                "name": "String"
              }
            },
            {
              "defaultValue": null,
              "description": "Provide dagger access to the executed command\nDo not use this option unless you trust the command being executed\nThe command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM",
              "name": "experimentalPrivilegedNesting",
              "type": {
                "kind": "SCALAR",
                "name": "Boolean"
              }
            }
          ],
          "deprecationReason": "",
          "description": "This container after executing the specified command inside it",
          "isDeprecated": false,
          "name": "exec",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "OBJECT",
              "name": "Container"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Container"
    }
`
		tmpl := templateHelper(t)

		object := objectInit(t, fieldArgsTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := expectedFieldArgsType

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})
}
