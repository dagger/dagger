package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/util/parallel"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
)

type artifactObjectValue struct {
	ID   core.ID
	Type string `json:"__typename"`
}

type artifactValueResult struct {
	Artifact struct {
		URI  string
		Path []string
	}
	Value *artifactObjectValue
	Error *artifactExecutionError
}

type artifactExecutionItem struct {
	ID            core.ID
	URI           string
	Path          []string
	Directives    []string
	LoadError     string
	Workspace     *struct{ ID core.ID }
	DimensionKeys []struct{ Dimension string }
}

type artifactExecutionError struct {
	Message string
	Values  []struct{ Name, Value string }
}

func evaluateArtifacts(ctx context.Context, dag *dagger.Client, artifacts *core.Artifacts, failFast bool, maxConcurrency int) ([]artifactValueResult, error) {
	if maxConcurrency < 0 {
		return nil, fmt.Errorf("maxConcurrency must not be negative: %d", maxConcurrency)
	}
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Selection struct{ Items []artifactExecutionItem }
	}
	err = dag.Do(ctx, &dagger.Request{
		Query: `query($id: ID!) { selection: node(id: $id) { ... on Artifacts {
		 items { id uri(dimensionKeys: true) path directives loadError workspace { id } dimensionKeys { dimension } }
		} } }`, Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	results := make([]artifactValueResult, len(response.Selection.Items))
	jobs := parallel.New().WithFailFast(failFast).WithRollupLogs(true).WithRollupSpans(true)
	var limit *semaphore.Weighted
	if maxConcurrency > 0 {
		limit = semaphore.NewWeighted(int64(maxConcurrency))
	}
	var syncFields sync.Map
	for i, item := range response.Selection.Items {
		results[i].Artifact.URI, results[i].Artifact.Path = item.URI, item.Path
		isCheck := slices.ContainsFunc(item.DimensionKeys, func(key struct{ Dimension string }) bool { return key.Dimension == "type:Check" })
		remote := isCheck && checksScaleOut && item.LoadError == "" && item.Workspace != nil
		var attrs []attribute.KeyValue
		if slices.Contains(item.Directives, "generate") {
			attrs = append(attrs, attribute.String(telemetry.GeneratorNameAttr, item.URI))
		}
		jobs = jobs.WithTracing(!isCheck).WithJob(item.URI, func(ctx context.Context) error {
			err := ctx.Err()
			if err == nil && limit != nil && !remote {
				err = limit.Acquire(ctx, 1)
				if err == nil {
					defer limit.Release(1)
				}
			}
			if err == nil {
				err = evaluateArtifactItem(ctx, dag, item.ID, &results[i], &syncFields)
			}
			if err != nil {
				if results[i].Error != nil {
					return err
				}
				results[i].Error = &artifactExecutionError{Message: err.Error()}
				var extended interface {
					error
					Extensions() map[string]any
				}
				if errors.As(err, &extended) {
					for name, value := range extended.Extensions() {
						encoded, encodeErr := json.Marshal(value)
						if encodeErr == nil {
							results[i].Error.Values = append(results[i].Error.Values, struct{ Name, Value string }{name, string(encoded)})
						}
					}
				}
			}
			return err
		}, attrs...)
	}
	if err := jobs.Run(ctx); err != nil {
		for i := range results {
			if results[i].Value == nil && results[i].Error == nil {
				results[i].Error = &artifactExecutionError{Message: context.Canceled.Error()}
			}
		}
	}
	return results, nil
}

func evaluateArtifactItem(ctx context.Context, dag *dagger.Client, id core.ID, result *artifactValueResult, syncFields *sync.Map) error {
	var response struct {
		Node struct {
			Value *artifactObjectValue
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Artifact { value { id __typename } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &response}); err != nil {
		return err
	}
	result.Value = response.Node.Value
	if result.Value == nil {
		return fmt.Errorf("artifact has no value")
	}
	typeName := result.Value.Type
	lookup, _ := syncFields.LoadOrStore(typeName, sync.OnceValues(func() (*introspection.Field, error) {
		var schema struct{ Type introspection.Type }
		err := dag.Do(ctx, &dagger.Request{
			Query: `query($type: String!) { type: __type(name: $type) { fields {
			 name args { defaultValue type { kind } }
			 type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }
			} } }`,
			Variables: map[string]any{"type": typeName},
		}, &dagger.Response{Data: &schema})
		if err != nil {
			return nil, err
		}
		for _, field := range schema.Type.Fields {
			if field.Name == "sync" && !slices.ContainsFunc(field.Args, func(arg introspection.InputValue) bool {
				return arg.TypeRef.Kind == introspection.TypeKindNonNull && arg.DefaultValue == nil
			}) {
				return field, nil
			}
		}
		return nil, nil
	}))
	syncField, err := lookup.(func() (*introspection.Field, error))()
	if err != nil || syncField == nil {
		return err
	}
	returnType := syncField.TypeRef
	for returnType.OfType != nil {
		returnType = returnType.OfType
	}
	selection := "sync"
	retain := syncField.TypeRef.IsObject() && returnType.Name == typeName
	if retain {
		selection += " { id"
		if typeName == "Check" {
			selection += " pass error { message values { name value } }"
		}
		selection += " }"
	} else if returnType.Kind == introspection.TypeKindObject || returnType.Kind == introspection.TypeKindInterface || returnType.Kind == introspection.TypeKindUnion {
		selection += " { __typename }"
	}
	if !retain {
		return dag.Do(ctx, &dagger.Request{
			Query:     `query($id: ID!) { node(id: $id) { ... on ` + typeName + ` { ` + selection + ` } } }`,
			Variables: map[string]any{"id": result.Value.ID},
		}, &dagger.Response{})
	}
	var completed struct {
		Node struct {
			Sync struct {
				ID    core.ID
				Pass  bool
				Error *artifactExecutionError
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on ` + typeName + ` { ` + selection + ` } } }`,
		Variables: map[string]any{"id": result.Value.ID},
	}, &dagger.Response{Data: &completed})
	if err != nil {
		return err
	}
	if completed.Node.Sync.ID != "" {
		result.Value.ID = completed.Node.Sync.ID
	}
	if typeName == "Check" && !completed.Node.Sync.Pass {
		result.Error = completed.Node.Sync.Error
		if result.Error == nil {
			result.Error = &artifactExecutionError{Message: "check failed"}
		}
		return errors.New(result.Error.Message)
	}
	return nil
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

func artifactWorkspaceConfig(ctx context.Context, ws *core.Workspace) (*workspace.Config, error) {
	config, err := ws.ConfigRead(ctx, core.WorkspaceConfigReadOpts{Effective: true})
	if err != nil {
		return nil, err
	}
	return workspace.ParseConfig([]byte(config))
}

// commandArtifacts applies shared filters to one selection of paths.
func commandArtifacts(ctx context.Context, dag *dagger.Client, ws *core.Workspace, cmd *cobra.Command, addresses []string, strict bool, keys ...dagaddress.Pair) (*core.Artifacts, error) {
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return nil, err
	}
	workspaceID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	ws = core.Ref[*core.Workspace](core.NewQuery(dag), workspaceID)
	for _, address := range parsed {
		if len(address.Query) == 0 {
			continue
		}
		defs, err := artifactDimensions(ctx, dag, ws.Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{address}, keys...)}))
		if err != nil {
			return nil, err
		}
		if err := bindArtifactDimensions(address.Query, defs); err != nil {
			return nil, err
		}
	}
	shared, err := sharedArtifactFilters(parsed)
	if err != nil {
		return nil, err
	}
	shared.Query = append(shared.Query, keys...)
	selection := ws.Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, keys...)})
	if strict {
		if err := requireArtifactModules(ctx, dag, selection, shared.DimensionFilters()); err != nil {
			return nil, err
		}
	}
	if slices.ContainsFunc(shared.Query, func(p dagaddress.Pair) bool { return p.HasKey && strings.HasPrefix(p.Dimension, "type:") }) {
		targets, err := commandArtifactTargets(ctx, dag, cmd, selection)
		if err != nil {
			return nil, err
		}
		paths, err := readArtifactListPaths(ctx, dag, targets)
		if err != nil {
			return nil, err
		}
		selection, err = filterArtifactTypeKeys(selection, paths, shared)
		if err != nil {
			return nil, err
		}
	}
	return filterArtifactAddress(selection, shared), nil
}

func sharedArtifactFilters(addresses []*dagaddress.Address) (*dagaddress.Address, error) {
	var previous string
	for i, address := range addresses {
		types := slices.Clone(address.Types)
		slices.Sort(types)
		types = slices.Compact(types)
		filters := address.DimensionFilters()
		for i := range filters {
			slices.Sort(filters[i].Keys)
			filters[i].Keys = slices.Compact(filters[i].Keys)
		}
		slices.SortFunc(filters, func(a, b dagaddress.DimensionFilter) int { return strings.Compare(a.Dimension, b.Dimension) })
		encoded, err := json.Marshal(struct {
			Types      []string
			Dimensions []dagaddress.DimensionFilter
		}{types, filters})
		if err != nil {
			return nil, err
		}
		if i > 0 && string(encoded) != previous {
			return nil, fmt.Errorf("addresses with different filters require a union of workspace views; use shared filter flags")
		}
		previous = string(encoded)
	}
	return &dagaddress.Address{Types: slices.Clone(addresses[0].Types), Query: slices.Clone(addresses[0].Query)}, nil
}

func requireArtifactModules(ctx context.Context, dag *dagger.Client, selection *core.Artifacts, filters []dagaddress.DimensionFilter) error {
	// A failed module has no schema. Module selectors can exclude it; type
	// and collection selectors cannot prove that it has no matching artifacts.
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

func artifactLoadFailures(ctx context.Context, dag *dagger.Client, artifacts *core.Artifacts) ([]artifactLoadFailure, error) {
	// Read schema metadata so a collection's own load field stays deferred.
	artifacts = artifacts.FilterPathPattern("*/load")
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ PathDefinitions []artifactLoadFailure }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query ArtifactLoadErrors($id: ID!) {
  node(id: $id) { ... on Artifacts { pathDefinitions(typeAssertion: true) { uri loadError } } }
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

func commandArtifactsWithFlags(ctx context.Context, dag *dagger.Client, ws *core.Workspace, cmd *cobra.Command, addresses []string, strict bool) (*core.Artifacts, error) {
	keys := artifactKeyFlags(cmd)
	if len(keys) > 0 {
		parsed, err := parseArtifactAddresses(addresses)
		if err != nil {
			return nil, err
		}
		defs, err := artifactDimensions(ctx, dag, ws.Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, keys...)}))
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

func validateArtifactListFlags(cmd *cobra.Command) error {
	if isArtifactCommand(cmd) {
		list, _ := cmd.Flags().GetBool("list")
		if !list {
			for _, name := range []string{"all", "absolute", "abs", "format"} {
				if cmd.Flags().Changed(name) {
					return fmt.Errorf("--%s requires --list", name)
				}
			}
		}
	}
	if flag := cmd.Flags().Lookup("format"); flag != nil && (isArtifactCommand(cmd) || cmd.Name() == "list" || cmd.Annotations[artifactListType] != "" || cmd.Annotations[artifactListCollection] != "") {
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
