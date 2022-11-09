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
class CacheVolume extends BaseClient {

  /**
   * A unique identifier for this container
   */
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

export type HostWorkdirArgs = {
  exclude?: []string;
  include?: []string;
};

class Host extends BaseClient {

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
        "kind": "OBJECT",
        "name": "CacheVolume",
        "description": "",
        "fields": [
          {
            "name": "id",
            "description": "A unique identifier for this container",
            "args": [],
            "type": {
              "kind": "NON_NULL",
              "name": null,
              "ofType": {
                "kind": "SCALAR",
                "name": "CacheID",
                "ofType": null
              }
            },
            "isDeprecated": false,
            "deprecationReason": null
          }
        ],
        "inputFields": null,
        "interfaces": [],
        "enumValues": null,
        "possibleTypes": null
      },
      {
        "kind": "OBJECT",
        "name": "Host",
        "description": "",
	"fields": [
	{
		"name": "workdir",
		"description": "The current working directory on the host",
		"args": [
		{
			"name": "exclude",
			"description": "",
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
			},
			"defaultValue": null
		},
		{
			"name": "include",
			"description": "",
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
			},
			"defaultValue": null
		}
		],
		"type": {
			"kind": "NON_NULL",
			"name": null,
			"ofType": {
				"kind": "OBJECT",
				"name": "Directory",
				"ofType": null
			}
		},
		"isDeprecated": false,
		"deprecationReason": null
	}
	],
        "inputFields": null,
        "interfaces": [],
        "enumValues": null,
        "possibleTypes": null
      }
]
`
