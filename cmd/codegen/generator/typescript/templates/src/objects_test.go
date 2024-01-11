package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjects(t *testing.T) {
	cases := map[string]struct {
		in           string
		wantFilePath string
	}{
		"CacheVolume + Host": {objectsJSON, "testdata/objects_test_want.ts"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templateHelper(t)

			jsonData := c.in

			objects := objectsInit(t, jsonData)

			var b bytes.Buffer
			err := tmpl.ExecuteTemplate(&b, "objects", objects)

			want := updateAndGetFixtures(t, c.wantFilePath, b.String())
			require.NoError(t, err)
			require.Equal(t, want, b.String())
		})
	}
}

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
                  "name": "CacheVolumeID",
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
