package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type generatorsSchema struct{}

var _ SchemaResolvers = &generatorsSchema{}

func (s generatorsSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.GeneratorGroup]{
		dagql.Func("list", s.list).
			Doc("Return a list of individual generators and their details"),

		dagql.Func("run", s.run).
			Doc("Execute all selected generators"),

		dagql.NodeFunc("isEmpty", s.groupIsEmpty).
			IsPersistable().
			Doc("Whether the generated changeset from the last run is empty or not"),

		dagql.NodeFunc("changes", s.groupChanges).
			IsPersistable().
			Doc(`The combined changes from the last run of the generators`,
				`If any conflict occurs, for instance if the same file is modified by multiple generators,
				or if a file is both modified and deleted, an error is raised and the merge of the changesets will failed.`,
				`Set 'continueOnConflicts' flag to force to merge the changes in a 'last write wins' strategy.`).
			Args(
				dagql.Arg("onConflict").Doc(`Strategy to apply on conflicts between generators`),
			),

		dagql.NodeFunc("workspace", s.workspace).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("The workspace with the combined output from the last generator run").
			Args(dagql.Arg("onConflict").Doc("Strategy to apply on conflicts between generators")),

		dagql.Func("loadFailures", s.loadFailures).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Load failures tolerated while collecting the generators.`,
				`Empty unless a workspace module could not be loaded during an unscoped 'dagger generate' (no selector), where load failures are tolerated so the modules that do load still generate. Each entry is a human-readable error message. An explicit selector keeps failing hard instead.`),
	}.Install(srv)

	dagql.Fields[*core.Generator]{
		dagql.Func("name", s.name).
			Doc("Return the command name of the generator. Entrypoint targets omit the module prefix."),

		dagql.Func("path", s.path).
			Doc("The path of the generator within its module"),

		dagql.Func("description", s.description).
			Doc("Return the description of the generator"),

		dagql.Func("originalModule", s.originalModule).
			Doc("The module that defined the generator, or null for an engine-defined generator"),

		dagql.NodeFunc("changes", s.changes).
			Doc("The generated changeset from the last run"),

		dagql.Func("run", s.runSingleGenerator).
			Doc("Execute the generator"),

		dagql.NodeFunc("isEmpty", s.isEmpty).
			Doc("Whether changeset from the last generator run is empty or not"),
	}.Install(srv)
}

func (s generatorsSchema) list(ctx context.Context, parent *core.GeneratorGroup, args struct{}) ([]*core.Generator, error) {
	return parent.List(ctx), nil
}

func (s generatorsSchema) loadFailures(_ context.Context, parent *core.GeneratorGroup, args struct{}) ([]string, error) {
	messages := make([]string, 0, len(parent.LoadFailures))
	for _, failure := range parent.LoadFailures {
		messages = append(messages, failure.Message)
	}
	return messages, nil
}

func (s generatorsSchema) run(ctx context.Context, parent *core.GeneratorGroup, args struct{}) (*core.GeneratorGroup, error) {
	var specs []*core.SyntheticGeneratorSpec
	for _, generator := range parent.Generators {
		if generator.Synthetic != nil {
			specs = append(specs, generator.Synthetic)
		}
	}
	if len(specs) == 0 {
		return parent.Run(ctx, nil)
	}

	base, err := syntheticGeneratorWorkspace(ctx, parent.BoundWorkspace)
	if err != nil {
		return nil, err
	}
	aggregate, err := runSDKModuleGeneratorGraph(ctx, base, specs, true)
	if err != nil {
		return nil, err
	}
	aggregateRunner := func(context.Context, *core.SyntheticGeneratorSpec) (dagql.ObjectResult[*core.Workspace], dagql.ObjectResult[*core.Workspace], error) {
		return base, aggregate, nil
	}
	return parent.Run(ctx, aggregateRunner)
}

type generatorsGroupIsEmptyArgs struct {
}

func (s generatorsSchema) groupIsEmpty(ctx context.Context, parent dagql.ObjectResult[*core.GeneratorGroup], args generatorsGroupIsEmptyArgs) (dagql.Boolean, error) {
	empty, err := parent.Self().IsEmpty(ctx)
	return dagql.NewBoolean(empty), err
}

type generatorsGroupChangesArgs struct {
	OnConflict ChangesetsMergeConflict `default:"FAIL_EARLY"`
}

func (s generatorsSchema) groupChanges(ctx context.Context, parent dagql.ObjectResult[*core.GeneratorGroup], args generatorsGroupChangesArgs) (dagql.ObjectResult[*core.Changeset], error) {
	changes, err := s.groupChangesAtRoot(ctx, parent.Self(), args.OnConflict)
	if err != nil {
		return changes, err
	}
	if ws := parent.Self().BoundWorkspace.Self(); ws != nil {
		return reRootChangesetToCwd(ctx, changes, ws.Cwd)
	}
	return changes, nil
}

// groupChangesAtRoot converts Workspace output only for the Changeset merge.
// All merge inputs use workspace-root paths, including regular generators.
func (s generatorsSchema) groupChangesAtRoot(ctx context.Context, group *core.GeneratorGroup, onConflict ChangesetsMergeConflict) (dagql.ObjectResult[*core.Changeset], error) {
	var merged dagql.ObjectResult[*core.Changeset]
	results, err := group.ChangeResults(ctx)
	if err != nil {
		return merged, err
	}
	ids := make(dagql.ArrayInput[dagql.ID[*core.Changeset]], 0, len(results))
	for _, result := range results {
		id, err := result.ID()
		if err != nil {
			return merged, err
		}
		ids = append(ids, dagql.NewID[*core.Changeset](id))
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return merged, err
	}
	if err := dag.Select(ctx, dag.Root(), &merged,
		dagql.Selector{Field: "changeset"},
		dagql.Selector{Field: "withChangesets", Args: []dagql.NamedInput{
			{Name: "changes", Value: ids},
			{Name: "onConflict", Value: onConflict},
		}},
	); err != nil {
		return merged, err
	}
	if err := group.VerifySkippedModules(ctx, results); err != nil {
		return merged, err
	}
	return merged, nil
}

func (s generatorsSchema) workspace(ctx context.Context, parent dagql.ObjectResult[*core.GeneratorGroup], args generatorsGroupChangesArgs) (dagql.ObjectResult[*core.Workspace], error) {
	group := parent.Self()
	base, err := syntheticGeneratorWorkspace(ctx, group.BoundWorkspace)
	if err != nil {
		return base, err
	}
	if group.BoundWorkspace.Self() == nil {
		group = group.Clone()
		group.BoundWorkspace = base
	}
	generated := base
	regular := false
	for _, generator := range group.Generators {
		if !generator.Completed {
			return base, fmt.Errorf("generator %q must be run before querying workspace", generator.Name())
		}
		if generator.Synthetic == nil {
			regular = true
		} else {
			generated = generator.WorkspaceResult
			if generated.Self() == nil {
				return base, fmt.Errorf("generator %q did not produce a workspace result", generator.Name())
			}
		}
	}
	if !regular {
		if len(group.LoadFailures) > 0 {
			changes, err := (&workspaceSchema{}).workspaceChangesBetween(ctx, base, generated)
			if err != nil {
				return base, err
			}
			if err := group.VerifySkippedModules(ctx, []dagql.ObjectResult[*core.Changeset]{changes}); err != nil {
				return base, err
			}
		}
		return generated, nil
	}
	changes, err := s.groupChangesAtRoot(ctx, group, args.OnConflict)
	if err != nil {
		return base, err
	}
	return (&workspaceSchema{}).workspaceWithChangeset(ctx, base, changes)
}

func (s generatorsSchema) name(_ context.Context, parent *core.Generator, args struct{}) (string, error) {
	return parent.Name(), nil
}

func (s generatorsSchema) path(_ context.Context, parent *core.Generator, args struct{}) ([]string, error) {
	return parent.Path(), nil
}

func (s generatorsSchema) description(_ context.Context, parent *core.Generator, args struct{}) (string, error) {
	return parent.Description(), nil
}

func (s generatorsSchema) originalModule(_ context.Context, parent *core.Generator, args struct{}) (dagql.Nullable[*core.Module], error) {
	module := parent.OriginalModule()
	if module == nil {
		return dagql.Null[*core.Module](), nil
	}
	return dagql.NonNull(module), nil
}

func (s generatorsSchema) changes(ctx context.Context, parent dagql.ObjectResult[*core.Generator], args struct{}) (dagql.ObjectResult[*core.Changeset], error) {
	changes, err := parent.Self().RequireChangesResult(ctx, "changes")
	if err != nil {
		return changes, err
	}
	if ws := parent.Self().WorkspaceBase.Self(); ws != nil {
		return reRootChangesetToCwd(ctx, changes, ws.Cwd)
	}
	return changes, nil
}

func (s generatorsSchema) runSingleGenerator(ctx context.Context, parent *core.Generator, args struct{}) (*core.Generator, error) {
	return parent.Run(ctx, runSyntheticSDKGenerator)
}

func (s generatorsSchema) isEmpty(ctx context.Context, parent dagql.ObjectResult[*core.Generator], args struct{}) (dagql.Boolean, error) {
	changes, err := parent.Self().RequireChanges(ctx, "isEmpty")
	if err != nil {
		return false, err
	}
	empty, err := changes.IsEmpty(ctx)
	return dagql.NewBoolean(empty), err
}
