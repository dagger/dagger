package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type QuerySuite struct{}

func TestQuery(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(QuerySuite{})
}

func (QuerySuite) TestSchemaJSONFile(ctx context.Context, t *testctx.T) {
	schemaRes, err := testutil.Query[struct {
		SchemaJSONFile struct {
			ID core.FileID
		} `json:"__schemaJSONFile"`
	}](t,
		`{
			__schemaJSONFile {
				id
			}
		}`, nil)
	require.NoError(t, err)
	id := schemaRes.SchemaJSONFile.ID

	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](t,
		`query Test($id: FileID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedFile(path: "/mnt/the-schema-file", source: $id) {
						withExec(args: ["cat", "/mnt/the-schema-file"]) {
							stdout
						}
					}
				}
			}
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	schemaJSON := res.Container.From.WithMountedFile.WithExec.Stdout

	var schema map[string]any
	json.Unmarshal([]byte(schemaJSON), &schema)
	_, found := schema["__schema"]
	require.True(t, found)
}
