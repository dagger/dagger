package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vito/progrock"
)

type ModuleFunction struct {
	root    *Query
	mod     *Module
	objDef  *ObjectTypeDef // may be nil for special functions like the module definition function call
	runtime *Container

	metadata   *Function
	returnType ModType
	args       map[string]*UserModFunctionArg
}

type UserModFunctionArg struct {
	metadata *FunctionArg
	modType  ModType
}

func newModFunction(
	ctx context.Context,
	root *Query,
	mod *Module,
	modID *idproto.ID,
	objDef *ObjectTypeDef,
	runtime *Container,
	metadata *Function,
) (*ModuleFunction, error) {
	returnType, ok, err := mod.ModTypeFor(ctx, metadata.ReturnType, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find mod type for function %q return type: %q", metadata.Name, metadata.ReturnType.ToType())
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadata := range metadata.Args {
		argModType, ok, err := mod.ModTypeFor(ctx, argMetadata.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for function %q arg %q type: %w", metadata.Name, argMetadata.Name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for function %q arg %q type", metadata.Name, argMetadata.Name)
		}
		argTypes[argMetadata.Name] = &UserModFunctionArg{
			metadata: argMetadata,
			modType:  argModType,
		}
	}

	return &ModuleFunction{
		root:       root,
		mod:        mod,
		objDef:     objDef,
		runtime:    runtime,
		metadata:   metadata,
		returnType: returnType,
		args:       argTypes,
	}, nil
}

type CallOpts struct {
	Inputs         []CallInput
	ParentVal      map[string]any
	Cache          bool
	Pipeline       pipeline.Path
	SkipSelfSchema bool
}

type CallInput struct {
	Name  string
	Value dagql.Typed
}

func (fn *ModuleFunction) recordCall(ctx context.Context) {
	mod := fn.mod
	if fn.metadata.Name == "" {
		return
	}
	props := map[string]string{
		"target_function": fn.metadata.Name,
	}
	moduleAnalyticsProps(mod, "target_", props)
	if caller, err := mod.Query.CurrentModule(ctx); err == nil {
		props["caller_type"] = "module"
		moduleAnalyticsProps(caller, "caller_", props)
	} else if analytics.IsInternal(ctx) {
		props["caller_type"] = "internal"
	} else {
		props["caller_type"] = "direct"
	}
	analytics.Ctx(ctx).Capture(ctx, "module_call", props)
}

func (fn *ModuleFunction) Call(ctx context.Context, caller *idproto.ID, opts *CallOpts) (t dagql.Typed, rerr error) {
	mod := fn.mod

	lg := bklog.G(ctx).WithField("module", mod.Name()).WithField("function", fn.metadata.Name)
	if fn.objDef != nil {
		lg = lg.WithField("object", fn.objDef.Name)
	}
	ctx = bklog.WithLogger(ctx, lg)

	// Capture analytics for the function call.
	// Calls without function name are internal and excluded.
	fn.recordCall(ctx)

	callInputs := make([]*FunctionCallArgValue, len(opts.Inputs))
	hasArg := map[string]bool{}
	for i, input := range opts.Inputs {
		normalizedName := gqlArgName(input.Name)
		arg, ok := fn.args[normalizedName]
		if !ok {
			return nil, fmt.Errorf("failed to find arg %q", input.Name)
		}

		name := arg.metadata.OriginalName

		converted, err := arg.modType.ConvertToSDKInput(ctx, input.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %q: %w", input.Name, err)
		}

		encoded, err := json.Marshal(converted)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal arg %q: %w", input.Name, err)
		}

		callInputs[i] = &FunctionCallArgValue{
			Name:  name,
			Value: encoded,
		}

		hasArg[name] = true
	}

	for _, arg := range fn.metadata.Args {
		name := arg.OriginalName
		if hasArg[name] || arg.DefaultValue == nil {
			continue
		}
		callInputs = append(callInputs, &FunctionCallArgValue{
			Name:  name,
			Value: arg.DefaultValue,
		})
	}

	callerDigestInputs := []string{}
	{
		callerIDDigest, err := caller.Digest() // FIXME(vito) canonicalize, once all that's implemented
		if err != nil {
			return nil, fmt.Errorf("failed to get caller digest: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, callerIDDigest.String())
	}
	if !opts.Cache {
		// use the ServerID so that we bust cache once-per-session
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, clientMetadata.ServerID)
	}

	callerDigest := digest.FromString(strings.Join(callerDigestInputs, " "))

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("caller_digest", callerDigest.String()))
	bklog.G(ctx).Debug("function call")
	defer func() {
		bklog.G(ctx).Debug("function call done")
		if rerr != nil {
			bklog.G(ctx).WithError(rerr).Error("function call errored")
		}
	}()

	ctr := fn.runtime

	metaDir := NewScratchDirectory(mod.Query, mod.Query.Platform)
	ctr, err := ctr.WithMountedDirectory(ctx, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, ContainerExecOpts{
		ModuleCallerDigest:            callerDigest,
		ExperimentalPrivilegedNesting: true,
		NestedInSameSession:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	parentJSON, err := json.Marshal(opts.ParentVal)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal parent value: %w", err)
	}

	callMeta := &FunctionCall{
		Query:     fn.root,
		Name:      fn.metadata.OriginalName,
		Parent:    parentJSON,
		InputArgs: callInputs,
	}
	if fn.objDef != nil {
		callMeta.ParentName = fn.objDef.OriginalName
	}

	var deps *ModDeps
	if opts.SkipSelfSchema {
		// Only serve the APIs of the deps of this module. This is currently only needed for the special
		// case of the function used to get the definition of the module itself (which can't obviously
		// be served the API its returning the definition of).
		deps = mod.Deps
	} else {
		// by default, serve both deps and the module's own API to itself
		deps = mod.Deps.Prepend(mod)
	}

	err = mod.Query.RegisterFunctionCall(callerDigest, deps, fn.mod, callMeta,
		progrock.FromContext(ctx).Parent)
	if err != nil {
		return nil, fmt.Errorf("failed to register function call: %w", err)
	}

	_, err = ctr.Evaluate(ctx)
	if err != nil {
		if fn.metadata.OriginalName == "" {
			return nil, fmt.Errorf("call constructor: %w", err)
		} else {
			return nil, fmt.Errorf("call function %q: %w", fn.metadata.OriginalName, err)
		}
	}

	ctrOutputDir, err := ctr.Directory(ctx, modMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get function output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate function: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("function returned nil result")
	}

	// TODO: if any error happens below, we should really prune the cache of the result, otherwise
	// we can end up in a state where we have a cached result with a dependency blob that we don't
	// guarantee the continued existence of...

	// Read the output of the function
	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: modMetaOutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read function output file: %w", err)
	}

	var returnValue any
	dec := json.NewDecoder(strings.NewReader(string(outputBytes)))
	dec.UseNumber()
	if err := dec.Decode(&returnValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	returnValueTyped, err := fn.returnType.ConvertFromSDKResult(ctx, returnValue)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return value: %w", err)
	}

	if err := fn.linkDependencyBlobs(ctx, result, returnValueTyped); err != nil {
		return nil, fmt.Errorf("failed to link dependency blobs: %w", err)
	}

	return returnValueTyped, nil
}

func (fn *ModuleFunction) ReturnType() (ModType, error) {
	return fn.returnType, nil
}

func (fn *ModuleFunction) ArgType(argName string) (ModType, error) {
	arg, ok := fn.args[gqlArgName(argName)]
	if !ok {
		return nil, fmt.Errorf("failed to find arg %q", argName)
	}
	return arg.modType, nil
}

func moduleAnalyticsProps(mod *Module, prefix string, props map[string]string) {
	props[prefix+"module_name"] = mod.Name()

	source := mod.Source.Self
	switch source.Kind {
	case ModuleSourceKindLocal:
		local := source.AsLocalSource.Value
		props[prefix+"source_kind"] = "local"
		props[prefix+"local_subpath"] = local.RootSubpath
	case ModuleSourceKindGit:
		git := source.AsGitSource.Value
		props[prefix+"source_kind"] = "git"
		props[prefix+"git_symbolic"] = git.Symbolic()
		props[prefix+"git_clone_url"] = git.CloneURL()
		props[prefix+"git_subpath"] = git.RootSubpath
		props[prefix+"git_version"] = git.Version
		props[prefix+"git_commit"] = git.Commit
	}
}

// If the result of a Function call contains IDs of resources, we need to
// ensure that the cache entry for the Function call is linked for the cache
// entries of those resources if those entries aren't reproducible. Right now,
// the only unreproducible output are local dir imports, which are represented
// as blob:// sources. linkDependencyBlobs finds all such blob:// sources and
// adds a cache lease on that blob in the content store to the cacheResult of
// the function call.
//
// If we didn't do this, then it would be possible for Buildkit to prune the
// content pointed to by the blob:// source without pruning the function call
// cache entry. That would result callers being able to evaluate the result of
// a function call but hitting an error about missing content.
func (fn *ModuleFunction) linkDependencyBlobs(ctx context.Context, cacheResult *buildkit.Result, value dagql.Typed) error {
	if value == nil {
		return nil
	}
	pbDefs, err := collectPBDefinitions(ctx, value)
	if err != nil {
		return fmt.Errorf("failed to collect pb definitions: %w", err)
	}
	dependencyBlobs := map[digest.Digest]*ocispecs.Descriptor{}
	for _, pbDef := range pbDefs {
		dag, err := buildkit.DefToDAG(pbDef)
		if err != nil {
			return fmt.Errorf("failed to convert pb definition to dag: %w", err)
		}
		blobs, err := dag.BlobDependencies()
		if err != nil {
			return fmt.Errorf("failed to get blob dependencies: %w", err)
		}
		for k, v := range blobs {
			dependencyBlobs[k] = v
		}
	}
	if err := cacheResult.Ref.AddDependencyBlobs(ctx, dependencyBlobs); err != nil {
		return fmt.Errorf("failed to add dependency blob: %w", err)
	}
	return nil
}
