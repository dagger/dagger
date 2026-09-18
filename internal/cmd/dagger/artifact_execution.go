package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	telemetry "github.com/dagger/otel-go"
)

type artifactValueResult struct {
	Artifact struct{ URI string }
	Value    *struct {
		ID   dagger.ID
		Type string `json:"__typename"`
	}
	Error *struct{ Message string }
}

func evaluateArtifacts(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts, failFast bool) ([]artifactValueResult, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Selection struct{ Values []artifactValueResult }
	}
	err = dag.Do(ctx, &dagger.Request{
		Query: `query ArtifactValues($id: ID!, $failFast: Boolean!) {
   selection: node(id: $id) { ... on Artifacts { values(failFast: $failFast) {
    artifact { uri } error { message } value { id __typename }
   } } }
  }`,
		Variables: map[string]any{"id": id, "failFast": failFast},
	}, &dagger.Response{Data: &response})
	return response.Selection.Values, err
}

func artifactResultErrors(results []artifactValueResult) error {
	var failures []error
	for _, result := range results {
		if result.Error != nil {
			failures = append(failures, fmt.Errorf("%s: %s", result.Artifact.URI, result.Error.Message))
		}
	}
	return errors.Join(failures...)
}

func artifactWorkspaceConfig(ctx context.Context, ws *dagger.Workspace) (*workspace.Config, error) {
	config, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{Effective: true})
	if err != nil {
		return nil, err
	}
	return workspace.ParseConfig([]byte(config))
}

// commandArtifacts keeps each address's filters scoped to its own path.
func commandArtifacts(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, addresses []string, strict bool) (*dagger.Artifacts, error) {
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return nil, err
	}
	all := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed)})
	if strict {
		failures, err := artifactLoadFailures(ctx, dag, all)
		if err != nil {
			return nil, err
		}
		if len(failures) > 0 {
			messages := make([]string, len(failures))
			for i, failure := range failures {
				messages[i] = failure.LoadError
			}
			return nil, fmt.Errorf("workspace modules could not be loaded: %s", strings.Join(messages, "\n"))
		}
	}
	filtered := false
	for _, address := range parsed {
		filtered = filtered || len(address.Types) > 0 || len(address.Query) > 0
	}
	if !filtered {
		return all, nil
	}
	var paths []string
	for _, address := range parsed {
		selection := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{address})})
		filter := *address
		filter.Path = ""
		filter.Absolute = false
		uris, err := artifactURIs(ctx, dag, selection.FilterURI(filter.String()))
		if err != nil {
			return nil, err
		}
		for _, uri := range uris {
			parsed, err := dagaddress.Parse(uri)
			if err != nil {
				return nil, err
			}
			paths = append(paths, parsed.Path)
		}
	}
	return all.FilterURI("dag://{" + strings.Join(paths, ",") + "}"), nil
}

type artifactLoadFailure struct{ URI, LoadError string }

func artifactLoadFailures(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) ([]artifactLoadFailure, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ Items []artifactLoadFailure }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query ArtifactLoadErrors($id: ID!) {
  node(id: $id) { ... on Artifacts { items { uri loadError } } }
 }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var failures []artifactLoadFailure
	for _, item := range response.Node.Items {
		if item.LoadError != "" {
			failures = append(failures, item)
		}
	}
	return failures, nil
}

func listArtifactSelection(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, out io.Writer) error {
	ctx, span := Tracer().Start(ctx, "list artifacts", telemetry.Encapsulate())
	defer span.End()
	id, err := selection.ID(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Node struct {
			Items []struct{ URI, Description string }
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) {
		node(id: $id) { ... on Artifacts { items { uri description } } }
	}`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return err
	}
	items := make([]commandListItem, 0, len(response.Node.Items))
	for _, item := range response.Node.Items {
		items = append(items, commandListItem{Name: item.URI, Comment: firstDescriptionLine(item.Description)})
	}
	return writeCommandList(out, items)
}
