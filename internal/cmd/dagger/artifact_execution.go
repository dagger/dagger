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
func commandArtifacts(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, cmd *cobra.Command, addresses []string, strict bool, keys ...dagaddress.Pair) (*dagger.Artifacts, error) {
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return nil, err
	}
	// currentWorkspace has a new identity on each call. Resolve it once so
	// address selections from this command can share a collection batch.
	workspaceID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	ws = dagger.Ref[*dagger.Workspace](dag, workspaceID)
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
		if slices.ContainsFunc(filter.Query, func(p dagaddress.Pair) bool { return p.HasKey && strings.HasPrefix(p.Dimension, "type:") }) {
			targets, err := commandArtifactTargets(dag, cmd, selection)
			if err != nil {
				return nil, err
			}
			schema, err := readArtifactListSchema(ctx, dag, targets)
			if err != nil {
				return nil, err
			}
			if err := resolveArtifactTypeKeys(schema.PathDefinitions, filter.Query); err != nil {
				return nil, err
			}
		}
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
	cmd.Flags().BoolP("all", "a", false, "Expand collections and list each item")
	cmd.Flags().StringP("format", "f", "cli", "Output `FORMAT`: table, link, or cli (requires --list)")
}

func isArtifactCommand(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "check", "up", "shell", "generate", "agent":
		return true
	}
	return false
}

func commandArtifactsWithFlags(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, cmd *cobra.Command, addresses []string, strict bool) (*dagger.Artifacts, error) {
	if cmd.Flags().Changed("format") {
		list, _ := cmd.Flags().GetBool("list")
		if !list {
			return nil, fmt.Errorf("--format requires --list")
		}
	}
	format, _ := cmd.Flags().GetString("format")
	if err := validateArtifactListFormat(format); err != nil {
		return nil, err
	}
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
	return commandArtifacts(ctx, dag, ws, cmd, addresses, strict, keys...)
}

func commandArtifactTargets(dag *dagger.Client, cmd *cobra.Command, artifacts *dagger.Artifacts) (*dagger.Artifacts, error) {
	switch cmd.Name() {
	case "check":
		return selectCommandChecks(dag, artifacts, cmd)
	case "shell":
		return artifacts.FilterTypes([]string{"Container", "Directory"}), nil
	case "agent":
		return artifacts.FilterAgentCommand(), nil
	case "generate":
		return artifacts.FilterGenerateCommand(), nil
	case "up":
		return artifacts.FilterUpCommand(), nil
	default:
		return nil, fmt.Errorf("command %q does not select artifacts", cmd.Name())
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
