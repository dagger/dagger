package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// QueryCloud evaluates a selection on an artifact's value in another engine.
// The response is data, not engine-local result handles.
func (a *Artifact) QueryCloud(ctx context.Context, arguments JSON, fields string, dest any) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}
	uri, err := a.URI(ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return true, err
	}
	client, remote, err := q.CloudEngineClient(ctx, "", uri, nil)
	if err != nil {
		return true, err
	}
	if !remote {
		return false, nil
	}
	defer func() { rerr = errors.Join(rerr, client.Close()) }()
	recipe, err := a.Workspace.RecipeID(ctx)
	if err != nil {
		return true, err
	}
	id, err := recipe.Encode()
	if err != nil {
		return true, err
	}
	var response struct {
		Node struct {
			Artifacts struct {
				FilterURI struct {
					Values []struct {
						Value json.RawMessage
						Error *Error
					}
				}
			}
		}
	}
	err = client.Dagger().Do(ctx, &dagger.Request{
		Query: `query($workspace: ID!, $path: String!, $uri: String!, $arguments: JSON!) {
		 node(id: $workspace) { ... on Workspace { artifacts(include: [$path]) {
		  filterUri(uri: $uri) { values(arguments: $arguments) { error { message values { name value } } value { ` + fields + ` } } }
		 } } }
		}`,
		Variables: map[string]any{"workspace": id, "path": strings.Join(a.Path, "/"), "uri": uri, "arguments": string(arguments)},
	}, &dagger.Response{Data: &response})
	if err != nil {
		return true, fmt.Errorf("evaluate %s in cloud engine: %w", uri, err)
	}
	results := response.Node.Artifacts.FilterURI.Values
	if len(results) != 1 {
		return true, fmt.Errorf("expected one result for %s, got %d", uri, len(results))
	}
	if results[0].Error != nil {
		return true, results[0].Error
	}
	return true, json.Unmarshal(results[0].Value, dest)
}
