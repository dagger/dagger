package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
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
func commandArtifacts(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, addresses []string, strict bool, keys ...dagaddress.Pair) (*dagger.Artifacts, error) {
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
	filtered := len(keys) > 0
	for _, address := range parsed {
		filtered = filtered || len(address.Types) > 0 || len(address.Query) > 0
	}
	if !filtered {
		return all, nil
	}
	var selected *dagger.Artifacts
	for _, address := range parsed {
		selection := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{address})})
		if len(address.Query) > 0 {
			defs, err := artifactDimensions(ctx, dag, selection)
			if err != nil {
				return nil, err
			}
			if err := bindArtifactDimensions(address.Query, defs); err != nil {
				return nil, err
			}
		}
		filter := *address
		filter.Path = ""
		filter.Absolute = false
		filter.Query = append(slices.Clone(address.Query), keys...)
		selection = selection.FilterURI(filter.String())
		if selected == nil {
			selected = selection
		} else {
			selected = selected.WithArtifacts(selection)
		}
	}
	return selected, nil
}

type artifactLoadFailure struct{ URI, LoadError string }

func artifactLoadFailures(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) ([]artifactLoadFailure, error) {
	// Synthetic load checks live at <module>/load. Narrow before enumerating
	// items, so looking for load errors does not construct unrelated collections.
	artifacts = artifacts.FilterURI("dag://*/load")
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

// Command list output uses the same dimension flags as command execution.
func registerCommandArtifactFlags(cmd *cobra.Command) {
	registerArtifactListFlags(cmd)
	cmd.Flags().BoolP("all", "a", false, "List each dimension key combination")
	cmd.Flags().StringArray("dimension-key", nil, "Keep a dimension key: DIMENSION=KEY (repeat for alternatives)")
}

func isArtifactCommand(cmd *cobra.Command) bool {
	switch cmd {
	case checksCmd, upCmd, shellCmd, generateCmd, agentCmd:
		return true
	}
	return false
}

func commandArtifactsWithFlags(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, cmd *cobra.Command, addresses []string, strict bool) (*dagger.Artifacts, error) {
	keys, err := artifactKeyFlags(cmd)
	if err != nil {
		return nil, err
	}
	if len(keys) > 0 {
		parsed, err := parseArtifactAddresses(addresses)
		if err != nil {
			return nil, err
		}
		defs, err := artifactDimensions(ctx, dag, ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed)}))
		if err != nil {
			return nil, err
		}
		if err := validateArtifactDimensionFlags(cmd, defs); err != nil {
			return nil, err
		}
		if err := bindArtifactDimensions(keys, defs); err != nil {
			return nil, err
		}
	}
	return commandArtifacts(ctx, dag, ws, addresses, strict, keys...)
}

func commandArtifactTargets(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, artifacts *dagger.Artifacts) (*dagger.Artifacts, error) {
	switch cmd.Name() {
	case "check":
		return selectCommandChecks(ctx, dag.CurrentWorkspace(), artifacts, cmd)
	case "shell":
		return artifacts.FilterTypes([]string{"Container", "Directory"}), nil
	default:
		return artifacts.FilterDirectives([]string{cmd.Name()}), nil
	}
}

// Preserve explicit check selection policy when a listed row is copied.
func artifactListReplayArgs(cmd *cobra.Command) []string {
	var args []string
	if cmd.Flags().Changed("generated") {
		args = append(args, "--generated="+cmd.Flag("generated").Value.String())
	}
	skip, _ := cmd.Flags().GetStringArray("skip")
	for _, pattern := range skip {
		args = append(args, "--skip="+pattern)
	}
	return args
}
