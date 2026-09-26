package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
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
	Error *struct {
		Message string
		// Values are the error extensions, such as an exec error's output.
		Values []struct{ Name, Value string }
	}
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
    artifact { uri } error { message values { name value } } value { id __typename }
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

// artifactResultErrorsWithOutput also shows the command and output of a
// failed exec. Use it where the frontend does not show the exec's own logs,
// such as a generator's deferred work.
func artifactResultErrorsWithOutput(results []artifactValueResult) error {
	var failures []error
	for _, result := range results {
		if result.Error == nil {
			continue
		}
		msg := result.Artifact.URI + ": " + result.Error.Message
		values := map[string]json.RawMessage{}
		for _, value := range result.Error.Values {
			values[value.Name] = json.RawMessage(value.Value)
		}
		var cmd []string
		if err := json.Unmarshal(values["cmd"], &cmd); err == nil && len(cmd) > 0 {
			msg += "\nCommand: " + strings.Join(cmd, " ")
		}
		for _, stream := range []string{"stdout", "stderr"} {
			var output string
			if err := json.Unmarshal(values[stream], &output); err == nil && strings.TrimSpace(output) != "" {
				msg += "\n" + strings.ToUpper(stream[:1]) + stream[1:] + ":\n" + strings.TrimRight(output, "\n")
			}
		}
		failures = append(failures, errors.New(msg))
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
	// address selections from this command share one workspace.
	workspaceID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	ws = dagger.Ref[*dagger.Workspace](dag, workspaceID)
	all := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, keys...)})
	filtered := len(keys) > 0
	for _, address := range parsed {
		filtered = filtered || len(address.Types) > 0 || len(address.Query) > 0
	}
	if !filtered {
		if strict {
			if err := requireArtifactModules(ctx, dag, all, nil); err != nil {
				return nil, err
			}
		}
		return all, nil
	}
	var selected *dagger.Artifacts
	for _, address := range parsed {
		selection := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{address}, keys...)})
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
		if strict {
			if err := requireArtifactModules(ctx, dag, selection, filter.DimensionFilters()); err != nil {
				return nil, err
			}
		}
		if slices.ContainsFunc(filter.Query, func(p dagaddress.Pair) bool { return p.HasKey && strings.HasPrefix(p.Dimension, "type:") }) {
			targets, err := commandArtifactTargets(ctx, dag, cmd, selection)
			if err != nil {
				return nil, err
			}
			paths, err := readArtifactListPaths(ctx, dag, targets)
			if err != nil {
				return nil, err
			}
			selection, err = filterArtifactTypeKeys(selection, paths, &filter)
			if err != nil {
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

func requireArtifactModules(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, filters []dagaddress.DimensionFilter) error {
	// A failed module has no schema. Module selectors can exclude it; type
	// selectors cannot prove that it has no matching artifacts.
	for _, filter := range filters {
		if filter.Dimension == artifact.ModuleDimension && filter.Keys != nil {
			selection = selection.FilterDimensionKeys(filter.Dimension, filter.Keys)
		}
	}
	failures, err := artifactLoadFailures(ctx, dag, selection)
	if err != nil {
		return err
	}
	if len(failures) == 0 {
		return nil
	}
	messages := make([]string, len(failures))
	for i, failure := range failures {
		messages[i] = failure.LoadError
	}
	return fmt.Errorf("workspace modules could not be loaded: %s", strings.Join(messages, "\n"))
}

type artifactLoadFailure struct{ URI, LoadError string }

func artifactLoadFailures(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) ([]artifactLoadFailure, error) {
	artifacts = artifacts.FilterURI("dag://*/load")
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ PathDefinitions []artifactLoadFailure }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query ArtifactLoadErrors($id: ID!) {
  node(id: $id) { ... on Artifacts { pathDefinitions { uri loadError } } }
 }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var failures []artifactLoadFailure
	for _, item := range response.Node.PathDefinitions {
		if item.LoadError != "" {
			failures = append(failures, item)
		}
	}
	return failures, nil
}

// Command list output uses the same dimension flags as command execution.
func registerCommandArtifactFlags(cmd *cobra.Command) {
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli (requires --list)")
}

func isArtifactCommand(cmd *cobra.Command) bool {
	return len(commandArtifactTypes(cmd)) > 0
}

func commandArtifactsWithFlags(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, cmd *cobra.Command, addresses []string, strict bool) (*dagger.Artifacts, error) {
	keys := artifactKeyFlags(cmd)
	if len(keys) > 0 {
		parsed, err := parseArtifactAddresses(addresses)
		if err != nil {
			return nil, err
		}
		defs, err := artifactDimensions(ctx, dag, ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, keys...)}))
		if err != nil {
			return nil, err
		}
		if err := bindArtifactDimensions(keys, defs); err != nil {
			return nil, err
		}
	}
	return commandArtifacts(ctx, dag, ws, cmd, addresses, strict, keys...)
}

func validateArtifactListFlags(cmd *cobra.Command) error {
	if isArtifactCommand(cmd) {
		list, _ := cmd.Flags().GetBool("list")
		if !list {
			for _, name := range []string{"absolute", "abs", "format"} {
				if cmd.Flags().Changed(name) {
					return fmt.Errorf("--%s requires --list", name)
				}
			}
		}
	}
	if flag := cmd.Flags().Lookup("format"); flag != nil && (isArtifactCommand(cmd) || cmd.Name() == "list" || cmd.Annotations[artifactListType] != "") {
		return validateArtifactListFormat(flag.Value.String())
	}
	return nil
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
