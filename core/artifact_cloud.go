package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
)

// The artifact recipe retains the whole selection, including its collection view.
func queryArtifactCloud(ctx context.Context, artifact dagql.ObjectResult[*Artifact], arguments JSON, fields string, dest any) error {
	a := artifact.Self()
	if a.Workspace.Self() == nil {
		return fmt.Errorf("artifact %s has no workspace to evaluate in a cloud engine", strings.Join(a.Path, "/"))
	}
	q, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	recipe, err := a.Workspace.RecipeID(ctx)
	if err != nil {
		return err
	}
	id, err := recipe.Encode()
	if err != nil {
		return err
	}
	var rules []artifactViewRule
	if a.View != nil {
		rules = a.View.state.Rules
	}
	encodedRules, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	client, err := q.SharedCloudArtifactClient(ctx, digest.FromString(id+string(encodedRules)).String())
	if err != nil {
		return err
	}
	recipe, err = artifact.RecipeID(ctx)
	if err != nil {
		return err
	}
	id, err = recipe.Encode()
	if err != nil {
		return err
	}
	var result struct {
		Node struct{ Value json.RawMessage }
	}
	err = client.Dagger().Do(ctx, &dagger.Request{
		Query: `query($artifact: ID!, $arguments: JSON!) {
		 node(id: $artifact) { ... on Artifact { value(arguments: $arguments) { ` + fields + ` } } }
		}`,
		Variables: map[string]any{"artifact": id, "arguments": string(arguments)},
	}, &dagger.Response{Data: &result})
	if err != nil {
		return err
	}
	return json.Unmarshal(result.Node.Value, dest)
}
