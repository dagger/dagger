// Package entrypointmodule implements the module entrypoint driver: it loads
// entrypoint.source as a manifest version 2 module and calls the
// ModuleEntrypoint interface on the object its constructor returns.
//
// Loading is recursive. The entrypoint module is itself a manifest version 2
// module, so its own entrypoint goes through a driver too, and every chain has
// to end at a built-in driver.
package entrypointmodule

import (
	"context"
	"fmt"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdk/entrypoint"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	telemetry "github.com/dagger/otel-go"
)

// chainKey carries the entrypoint directories already being loaded, so a chain
// that returns to one of them is reported instead of looping.
//
// dagql rejects a module that names itself before this chain sees the
// directory twice, with "recursive call detected". This chain covers the case
// dagql cannot see: a loop through entrypoints that are separate calls, where
// only the resolved directory repeats.
type chainKey struct{}

type chainEntry struct {
	// module names the module whose entrypoint this is, for the error message.
	module string
	// directory is the resolved entrypoint Directory ID, which is the identity
	// the spec detects cycles on.
	directory string
}

// pushChain records this entrypoint directory, and fails when the chain
// already contains it.
func pushChain(ctx context.Context, module, encoded string) (context.Context, error) {
	chain, _ := ctx.Value(chainKey{}).([]chainEntry)
	for _, entry := range chain {
		if entry.directory != encoded {
			continue
		}
		names := make([]string, 0, len(chain)+1)
		for _, entry := range chain {
			names = append(names, entry.module)
		}
		names = append(names, module)
		return nil, fmt.Errorf("module entrypoint cycle: %s", strings.Join(names, " -> "))
	}

	next := make([]chainEntry, len(chain), len(chain)+1)
	copy(next, chain)
	next = append(next, chainEntry{module: module, directory: encoded})
	return context.WithValue(ctx, chainKey{}, next), nil
}

// instance is an entrypoint module loaded into its own client schema, with its
// constructor already called.
type instance struct {
	dag       *dagql.Server
	object    dagql.AnyObjectResult
	workspace dagql.ObjectResult[*core.Workspace]
}

// load resolves entrypoint.source, loads it as a module, and constructs the
// object that implements ModuleEntrypoint.
//
// The module serves its own schema, so the entrypoint sees its own types and
// dependencies rather than those of the module it implements.
func load(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
) (*instance, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Dagger server for module entrypoint: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for module entrypoint: %w", err)
	}

	entrySrc, workspace, err := entrypoint.ResolveSource(ctx, dag, src)
	if err != nil {
		return nil, err
	}

	entryDir := entrySrc.Self().ContextDirectory
	entryDirID, err := entryDir.ID()
	if err != nil {
		return nil, fmt.Errorf("get module entrypoint directory ID: %w", err)
	}
	// Encode rather than Digest: a directory resolved through an address is a
	// handle-form ID, which has no digest.
	encodedDir, err := entryDirID.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode module entrypoint directory ID: %w", err)
	}
	ctx, err = pushChain(ctx, src.Self().ModuleName, encodedDir)
	if err != nil {
		return nil, err
	}

	// Load the directory as its own module source rather than reusing the
	// resolved source: that one is a clone of the module being implemented, so
	// it still carries this module's entrypoint and would name itself.
	var entryModSrc dagql.ObjectResult[*core.ModuleSource]
	if err := dag.Select(ctx, entryDir, &entryModSrc, dagql.Selector{
		Field: "asModuleSource",
		Args:  []dagql.NamedInput{{Name: "sourceRootPath", Value: dagql.NewString(".")}},
	}); err != nil {
		return nil, fmt.Errorf("read module entrypoint %q: %w", src.Self().Entrypoint.Source, err)
	}

	var mod dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, entryModSrc, &mod, dagql.Selector{Field: "asModule"}); err != nil {
		return nil, fmt.Errorf("load module entrypoint %q: %w", src.Self().Entrypoint.Source, err)
	}

	entryDag, err := dagql.NewServer(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("create module entrypoint server: %w", err)
	}
	entryDag.Around(core.AroundFunc)
	core.InstallCoreSchemaLoaders(entryDag)

	if err := core.NewUserMod(mod).Install(ctx, entryDag); err != nil {
		return nil, fmt.Errorf("install module entrypoint %q: %w", mod.Self().Name(), err)
	}
	defaultDeps, err := query.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("get default dependencies for module entrypoint %q: %w", mod.Self().Name(), err)
	}
	for _, dep := range defaultDeps.Mods() {
		if err := dep.Install(ctx, entryDag); err != nil {
			return nil, fmt.Errorf("install default dependency %q for module entrypoint %q: %w", dep.Name(), mod.Self().Name(), err)
		}
	}

	// The spec requires one constructor that works without supplied arguments,
	// so the entrypoint is always reachable as the module's root field.
	var object dagql.AnyObjectResult
	if err := entryDag.Select(ctx, entryDag.Root(), &object, dagql.Selector{
		Field: strcase.ToLowerCamel(mod.Self().Name()),
	}); err != nil {
		return nil, fmt.Errorf("construct module entrypoint %q: %w", mod.Self().Name(), err)
	}

	return &instance{dag: entryDag, object: object, workspace: workspace}, nil
}

// workspaceArg encodes the workspace argument shared by types and call.
func (inst *instance) workspaceArg() (dagql.NamedInput, error) {
	id, err := inst.workspace.ID()
	if err != nil {
		return dagql.NamedInput{}, fmt.Errorf("get module workspace ID: %w", err)
	}
	return dagql.NamedInput{Name: "workspace", Value: dagql.NewID[*core.Workspace](id)}, nil
}

// ModuleTypes calls types on a module entrypoint and builds the module from
// the type definitions it returns.
func ModuleTypes(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("get Dagger server for module entrypoint types: %w", err)
	}

	entry, err := load(ctx, src)
	if err != nil {
		return inst, err
	}
	workspaceArg, err := entry.workspaceArg()
	if err != nil {
		return inst, err
	}

	var entryTypeDefs dagql.ObjectResultArray[*core.TypeDef]
	if err := entry.dag.Select(ctx, entry.object, &entryTypeDefs, dagql.Selector{
		Field: "types",
		Args:  []dagql.NamedInput{workspaceArg},
	}); err != nil {
		return inst, fmt.Errorf("call module entrypoint types: %w", err)
	}

	// The type definitions belong to the entrypoint's schema. Carry them over
	// by ID, which is how any entrypoint hands typed values back to the engine.
	typeDefs := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(entryTypeDefs))
	for i, entryTypeDef := range entryTypeDefs {
		id, err := entryTypeDef.ID()
		if err != nil {
			return inst, fmt.Errorf("get module entrypoint type %d ID: %w", i, err)
		}
		typeDef, err := dagql.NewID[*core.TypeDef](id).Load(ctx, dag)
		if err != nil {
			return inst, fmt.Errorf("load module entrypoint type %d: %w", i, err)
		}
		typeDefs = append(typeDefs, typeDef)
	}

	if err := entrypoint.ValidateConstructors(typeDefs); err != nil {
		return inst, err
	}
	return entrypoint.ModuleFromTypeDefs(ctx, dag, typeDefs)
}

type moduleRuntime struct {
	modSource dagql.ObjectResult[*core.ModuleSource]
}

// NewRuntime returns the module entrypoint runtime.
func NewRuntime(source dagql.ObjectResult[*core.ModuleSource]) core.ModuleRuntime {
	return &moduleRuntime{modSource: source}
}

func (r *moduleRuntime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *moduleRuntime) Call(
	ctx context.Context,
	_ *engineutil.ExecutionMetadata,
	fnCall *core.FunctionCall,
	_ dagql.ObjectResult[*core.Module],
) (rerr error) {
	entry, err := load(ctx, r.modSource)
	if err != nil {
		return err
	}
	workspaceArg, err := entry.workspaceArg()
	if err != nil {
		return err
	}
	fnArgs, err := entrypoint.FunctionArgsJSON(fnCall.InputArgs)
	if err != nil {
		return err
	}

	ctx, span := core.Tracer(ctx).Start(ctx, "call module entrypoint", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	// The entrypoint's JSON result arrives as the scalar's string form.
	var result dagql.String
	if err := entry.dag.Select(ctx, entry.object, &result, dagql.Selector{
		Field: "call",
		Args: []dagql.NamedInput{
			workspaceArg,
			{Name: "receiverType", Value: dagql.String(fnCall.ParentName)},
			{Name: "receiverValue", Value: fnCall.Parent},
			{Name: "fnName", Value: dagql.String(fnCall.Name)},
			{Name: "fnArgs", Value: fnArgs},
		},
	}); err != nil {
		return fmt.Errorf("call module entrypoint: %w", err)
	}

	return fnCall.ReturnValue(ctx, core.JSON(result))
}

var _ core.ModuleRuntime = (*moduleRuntime)(nil)
