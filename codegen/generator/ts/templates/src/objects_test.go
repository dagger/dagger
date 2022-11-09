package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjects(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"CacheVolume + Host": {objectsJSON, wantObjects},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templateHelper(t, "objects", "object", "field", "return_solve", "input_args", "return", "object_comment", "field_comment", "types", "type")

			jsonData := c.in

			objects := objectsInit(t, jsonData)

			var b bytes.Buffer
			err := tmpl.ExecuteTemplate(&b, "objects", objects)

			require.NoError(t, err)
			require.Equal(t, c.want, b.String())
		})
	}
}

var wantObjects = `
/**
 * A directory whose contents persist across runs
 */
class CacheVolume extends BaseClient {

  async id(): Promise<Record<string, Scalars['CacheID']>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, Scalars['CacheID']>> = await this._compute()

    return response
  }
}

export type HostDirectoryArgs = {
  path: string;
  exclude?: []string;
  include?: []string;
};

export type HostEnvVariableArgs = {
  name: string;
};

export type HostWorkdirArgs = {
  exclude?: []string;
  include?: []string;
};

/**
 * Information about the host execution environment
 */
class Host extends BaseClient {

  /**
   * Access a directory on the host
   */
  directory(args: HostDirectoryArgs): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ]

    return new Directory(this._queryTree)
  }
  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
   */
  envVariable(args: HostEnvVariableArgs): HostVariable {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'envVariable',
      args
      }
    ]

    return new HostVariable(this._queryTree)
  }
  /**
   * The current working directory on the host
   */
  workdir(args: HostWorkdirArgs): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'workdir',
      args
      }
    ]

    return new Directory(this._queryTree)
  }
}
`

var objectsJSON = `
[
        {
          "description": "A directory whose contents persist across runs",
          "enumValues": null,
          "fields": [
            {
              "args": [],
              "deprecationReason": null,
              "description": "",
              "isDeprecated": false,
              "name": "id",
              "type": {
                "kind": "NON_NULL",
                "name": null,
                "ofType": {
                  "kind": "SCALAR",
                  "name": "CacheID",
                  "ofType": null
                }
              }
            }
          ],
          "inputFields": null,
          "interfaces": [],
          "kind": "OBJECT",
          "name": "CacheVolume",
          "possibleTypes": null
        },
        {
          "description": "Information about the host execution environment",
          "enumValues": null,
          "fields": [
            {
              "args": [
                {
                  "defaultValue": null,
                  "description": "",
                  "name": "path",
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
                  "defaultValue": null,
                  "description": "",
                  "name": "exclude",
                  "type": {
                    "kind": "LIST",
                    "name": null,
                    "ofType": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                },
                {
                  "defaultValue": null,
                  "description": "",
                  "name": "include",
                  "type": {
                    "kind": "LIST",
                    "name": null,
                    "ofType": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                }
              ],
              "deprecationReason": null,
              "description": "Access a directory on the host",
              "isDeprecated": false,
              "name": "directory",
              "type": {
                "kind": "NON_NULL",
                "name": null,
                "ofType": {
                  "kind": "OBJECT",
                  "name": "Directory",
                  "ofType": null
                }
              }
            },
            {
              "args": [
                {
                  "defaultValue": null,
                  "description": "",
                  "name": "name",
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
              ],
              "deprecationReason": null,
              "description": "Lookup the value of an environment variable. Null if the variable is not available.",
              "isDeprecated": false,
              "name": "envVariable",
              "type": {
                "kind": "OBJECT",
                "name": "HostVariable",
                "ofType": null
              }
            },
            {
              "args": [
                {
                  "defaultValue": null,
                  "description": "",
                  "name": "exclude",
                  "type": {
                    "kind": "LIST",
                    "name": null,
                    "ofType": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                },
                {
                  "defaultValue": null,
                  "description": "",
                  "name": "include",
                  "type": {
                    "kind": "LIST",
                    "name": null,
                    "ofType": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                }
              ],
              "deprecationReason": null,
              "description": "The current working directory on the host",
              "isDeprecated": false,
              "name": "workdir",
              "type": {
                "kind": "NON_NULL",
                "name": null,
                "ofType": {
                  "kind": "OBJECT",
                  "name": "Directory",
                  "ofType": null
                }
              }
            }
          ],
          "inputFields": null,
          "interfaces": [],
          "kind": "OBJECT",
          "name": "Host",
          "possibleTypes": null
        }
]
`
