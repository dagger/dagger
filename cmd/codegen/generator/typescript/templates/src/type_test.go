package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestType(t *testing.T) {
	t.Run("scalar", func(t *testing.T) {
		wantFile := "testdata/type_test_scalar_want.ts"

		var fieldArgsTypeJSON = `
      {
        "kind": "SCALAR"  ,
        "name": "Container",
        "description": "Hola"
    }
`
		tmpl := templateHelper(t)

		object := objectInit(t, fieldArgsTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("scalar multiline comment", func(t *testing.T) {
		wantFile := "testdata/type_test_scalar_multiline_comment_want.ts"

		var fieldArgsTypeJSON = `
    {
      "kind": "SCALAR",
      "name": "Container",
      "description": "Container type.\nA simple container definition."
    }
    `

		tmpl := templateHelper(t)

		object := objectInit(t, fieldArgsTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("input", func(t *testing.T) {
		var expectedInputType = `
export type BuildArg = {
  /**
   * Name description.
   */
  name: string
  value: string
}
`

		var fieldInputTypeJSON = `
	{
		"kind": "INPUT_OBJECT",
		"name": "BuildArg",
		"description": "foo",
		"inputFields": [
		  {
		    "name": "name",
		    "description": "Name description.",
		    "defaultValue": null,
		    "type": {
		      "kind": "NON_NULL",
		      "name": null,
		      "ofType": {
				"kind": "SCALAR",
				"name": "String",
				"ofType": null
			  }
		    }
		  },
		  {
		    "name": "value",
		    "description": "",
		    "defaultValue": null,
		    "type": {
		      "kind": "NON_NULL",
		      "name": null,
		      "ofType": {
				"kind": "SCALAR",
				"name": "String",
				"ofType": null
			  }
		    }
		  }
		]
	}
`
		tmpl := templateHelper(t)

		object := objectInit(t, fieldInputTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := expectedInputType

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("input deprecated field", func(t *testing.T) {
		var expectedInputType = `
export type DeprecatedInput = {
  /**
   * Field description.
   *
   * @deprecated Use otherField instead.
   */
  deprecatedField?: string
}
`

		var deprecatedInputTypeJSON = `
	{
	  "kind": "INPUT_OBJECT",
	  "name": "DeprecatedInput",
	  "description": "foo",
	  "inputFields": [
	    {
	      "name": "deprecatedField",
	      "description": "Field description.",
	      "isDeprecated": true,
	      "deprecationReason": "Use otherField instead.",
	      "defaultValue": null,
	      "type": {
	        "kind": "SCALAR",
	        "name": "String",
	        "ofType": null
	      }
	    }
	  ]
	}
`

		tmpl := templateHelper(t)

		object := objectInit(t, deprecatedInputTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := expectedInputType

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("args", func(t *testing.T) {
		wantFile := "testdata/type_test_args_want.ts"

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

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("args deprecated", func(t *testing.T) {
		wantFile := "testdata/type_test_args_deprecated_want.ts"

		var fieldArgsDeprecatedJSON = `
    {
      "description": "Container with deprecated args",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "Path of the configuration file",
              "isDeprecated": false,
              "name": "path",
              "type": {
                "kind": "NON_NULL",
                "ofType": {
                  "kind": "SCALAR",
                  "name": "String"
                }
              }
            },
            {
              "defaultValue": null,
              "description": "Expand template variables before applying",
              "isDeprecated": true,
              "deprecationReason": "Templates are expanded automatically.",
              "name": "expand",
              "type": {
                "kind": "SCALAR",
                "name": "Boolean"
              }
            }
          ],
          "deprecationReason": "",
          "description": "Apply configuration to the container",
          "isDeprecated": false,
          "name": "apply",
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

		object := objectInit(t, fieldArgsDeprecatedJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("args deprecated no description", func(t *testing.T) {
		wantFile := "testdata/type_test_args_deprecated_no_description_want.ts"

		var fieldArgsDeprecatedNoDescJSON = `
    {
      "description": "Container with deprecated args",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "",
              "isDeprecated": true,
              "deprecationReason": "Templates are expanded automatically.",
              "name": "expand",
              "type": {
                "kind": "SCALAR",
                "name": "Boolean"
              }
            }
          ],
          "deprecationReason": "",
          "description": "Apply configuration to the container",
          "isDeprecated": false,
          "name": "apply",
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

		object := objectInit(t, fieldArgsDeprecatedNoDescJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})

	t.Run("interface optional arg deprecated", func(t *testing.T) {
		wantFile := "testdata/type_test_interface_optional_arg_deprecated_want.ts"

		var interfaceOptionalArgDeprecatedJSON = `
    {
      "description": "Test interface with deprecated method",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "",
              "isDeprecated": true,
              "deprecationReason": "Not needed anymore.",
              "name": "bar",
              "type": {
                "kind": "SCALAR",
                "name": "Int"
              }
            }
          ],
          "deprecationReason": "Use Bar instead.",
          "description": "",
          "isDeprecated": true,
          "name": "foo",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        }
      ],
      "kind": "INTERFACE",
      "name": "TestFooer"
    }
`

		tmpl := templateHelper(t)

		object := objectInit(t, interfaceOptionalArgDeprecatedJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})
}

func TestTypeEnum(t *testing.T) {
	t.Run("with-directive", func(t *testing.T) {
		var enumJSON = `  {
    "description": "Compression algorithm to use for image layers.",
    "directives": [],
    "enumValues": [
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Gzip\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "Gzip"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Zstd\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "Zstd"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"EStarGZ\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "EStarGZ"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Uncompressed\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "Uncompressed"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Gzip\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "GZIP"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Zstd\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "ZSTD"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"EStarGZ\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "ESTARGZ"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [
          {
            "args": [
              {
                "name": "value",
                "value": "\"Uncompressed\""
              }
            ],
            "name": "enumValue"
          }
        ],
        "isDeprecated": false,
        "name": "UNCOMPRESSED"
      }
    ],
    "fields": [],
    "inputFields": [],
    "interfaces": [],
    "kind": "ENUM",
    "name": "ImageLayerCompression",
    "possibleTypes": []
  }`

		wantFile := "testdata/type_test_enum_with_directive_want.ts"
		tmpl := templateHelper(t)

		object := objectInit(t, enumJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)
		require.NoError(t, err)

		want := updateAndGetFixtures(t, wantFile, b.String())
		require.Equal(t, want, b.String())
	})

	t.Run("no-directive", func(t *testing.T) {
		var enumJSON = `  {
    "description": "Transport layer network protocol associated to a port.",
    "directives": [],
    "enumValues": [
      {
        "deprecationReason": null,
        "description": "",
        "directives": [],
        "isDeprecated": false,
        "name": "TCP"
      },
      {
        "deprecationReason": null,
        "description": "",
        "directives": [],
        "isDeprecated": false,
        "name": "UDP"
      }
    ],
    "fields": [],
    "inputFields": [],
    "interfaces": [],
    "kind": "ENUM",
    "name": "NetworkProtocol",
    "possibleTypes": []
  }`

		wantFile := "testdata/type_test_enum_without_directive_want.ts"
		tmpl := templateHelper(t)

		object := objectInit(t, enumJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)
		require.NoError(t, err)

		want := updateAndGetFixtures(t, wantFile, b.String())
		require.Equal(t, want, b.String())
	})

	t.Run("value deprecated", func(t *testing.T) {
		wantFile := "testdata/type_test_enum_value_deprecated_want.ts"

		var enumValueDeprecatedJSON = `{
  "description": "",
  "directives": [],
  "enumValues": [
    {
      "deprecationReason": "Use ModeV2 instead.",
      "description": "",
      "directives": [],
      "isDeprecated": true,
      "name": "VALUE"
    }
  ],
  "kind": "ENUM",
  "name": "Mode"
}
`

		tmpl := templateHelper(t)

		object := objectInit(t, enumValueDeprecatedJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "type", object)

		want := updateAndGetFixtures(t, wantFile, b.String())

		require.NoError(t, err)
		require.Equal(t, want, b.String())
	})
}
