package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/graphql"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
)

type UserModFunction struct {
	api     *APIServer
	mod     *UserMod
	obj     *UserModObject // may be nil for special functions like the module definition function call
	runtime *core.Container

	metadata   *core.Function
	returnType ModType
	args       map[string]*UserModFunctionArg
}

type UserModFunctionArg struct {
	metadata *core.FunctionArg
	modType  ModType
}

func newModFunction(
	ctx context.Context,
	mod *UserMod,
	obj *UserModObject,
	runtime *core.Container,
	metadata *core.Function,
) (*UserModFunction, error) {
	returnType, ok, err := mod.ModTypeFor(ctx, metadata.ReturnType, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find mod type for function %q return type", metadata.Name)
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

	return &UserModFunction{
		api:        mod.api,
		mod:        mod,
		obj:        obj,
		runtime:    runtime,
		metadata:   metadata,
		returnType: returnType,
		args:       argTypes,
	}, nil
}

func (fn *UserModFunction) Digest() digest.Digest {
	inputs := []string{
		fn.mod.DagDigest().String(),
		fn.metadata.Name,
	}
	if fn.obj != nil {
		inputs = append(inputs, fn.obj.typeDef.AsObject.Name)
	}
	return digest.FromString(strings.Join(inputs, " "))
}

func (fn *UserModFunction) Schema(ctx context.Context) (*ast.FieldDefinition, graphql.FieldResolveFn, error) {
	fnName := gqlFieldName(fn.metadata.Name)
	var objFnName string
	if fn.obj != nil {
		objFnName = fmt.Sprintf("%s.%s", fn.obj.typeDef.AsObject.Name, fnName)
	} else {
		objFnName = fnName
	}

	returnASTType, err := typeDefToASTType(fn.metadata.ReturnType, false)
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a type from another (non-core) module, which is currently not allowed
	sourceMod := fn.returnType.SourceMod()
	if sourceMod != nil && sourceMod.Name() != coreModuleName && sourceMod.DagDigest() != fn.mod.DagDigest() {
		var objName string
		if fn.obj != nil {
			objName = fn.obj.typeDef.AsObject.OriginalName
		}
		return nil, nil, fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
			objName,
			fn.metadata.OriginalName,
			sourceMod.Name(),
		)
	}

	fieldDef := &ast.FieldDefinition{
		Name:        fnName,
		Description: formatGqlDescription(fn.metadata.Description),
		Type:        returnASTType,
	}

	for _, argMetadata := range fn.metadata.Args {
		arg, ok := fn.args[argMetadata.Name]
		if !ok {
			return nil, nil, fmt.Errorf("failed to find arg %q", argMetadata.Name)
		}

		argASTType, err := typeDefToASTType(argMetadata.TypeDef, true)
		if err != nil {
			return nil, nil, err
		}

		// Check if this is a type from another (non-core) module, which is currently not allowed
		sourceMod := arg.modType.SourceMod()
		if sourceMod != nil && sourceMod.Name() != coreModuleName && sourceMod.DagDigest() != fn.mod.DagDigest() {
			var objName string
			if fn.obj != nil {
				objName = fn.obj.typeDef.AsObject.OriginalName
			}
			return nil, nil, fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
				objName,
				fn.metadata.OriginalName,
				argMetadata.OriginalName,
				sourceMod.Name(),
			)
		}

		defaultValue, err := astDefaultValue(argMetadata.TypeDef, argMetadata.DefaultValue)
		if err != nil {
			return nil, nil, err
		}
		argDef := &ast.ArgumentDefinition{
			Name:         gqlArgName(argMetadata.Name),
			Description:  formatGqlDescription(argMetadata.Description),
			Type:         argASTType,
			DefaultValue: defaultValue,
		}
		fieldDef.Arguments = append(fieldDef.Arguments, argDef)
	}

	resolver := ToResolver(func(ctx context.Context, parent any, args map[string]any) (_ any, rerr error) {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("panic in %s: %s %s", objFnName, r, string(debug.Stack()))
			}
		}()

		var callInput []*core.CallInput
		for k, v := range args {
			callInput = append(callInput, &core.CallInput{
				Name:  k,
				Value: v,
			})
		}
		return fn.Call(ctx, &CallOpts{
			Inputs:    callInput,
			ParentVal: parent,
		})
	})

	return fieldDef, resolver, nil
}

type CallOpts struct {
	Inputs         []*core.CallInput
	ParentVal      any
	Cache          bool
	Pipeline       pipeline.Path
	SkipSelfSchema bool
}

func (fn *UserModFunction) Call(ctx context.Context, opts *CallOpts) (any, error) {
	lg := bklog.G(ctx).WithField("module", fn.mod.Name()).WithField("function", fn.metadata.Name)
	if fn.obj != nil {
		lg = lg.WithField("object", fn.obj.typeDef.AsObject.Name)
	}
	ctx = bklog.WithLogger(ctx, lg)

	callerDigestInputs := []string{fn.Digest().String()}

	parentVal := opts.ParentVal
	if fn.obj != nil {
		// serialize the parentVal so it can be added to the cache key
		parentBytes, err := json.Marshal(parentVal)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parent value: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, string(parentBytes))

		parentVal, err = fn.obj.ConvertToSDKInput(ctx, parentVal)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parent value: %w", err)
		}
	}

	for _, input := range opts.Inputs {
		normalizedName := gqlArgName(input.Name)
		arg, ok := fn.args[normalizedName]
		if !ok {
			return nil, fmt.Errorf("failed to find arg %q", input.Name)
		}
		input.Name = arg.metadata.OriginalName

		var err error
		input.Value, err = arg.modType.ConvertToSDKInput(ctx, input.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %q: %w", input.Name, err)
		}

		dgst, err := input.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get arg digest: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, dgst.String())
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
	}()

	ctr := fn.runtime

	metaDir := core.NewScratchDirectory(opts.Pipeline, fn.api.platform)
	ctr, err := ctr.WithMountedDirectory(ctx, fn.api.bk, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, fn.api.bk, fn.api.progSockPath, fn.api.platform, core.ContainerExecOpts{
		ModuleCallerDigest:            callerDigest,
		ExperimentalPrivilegedNesting: true,
		NestedInSameSession:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	callMeta := &core.FunctionCall{
		Name:      fn.metadata.OriginalName,
		Parent:    parentVal,
		InputArgs: opts.Inputs,
	}
	if fn.obj != nil {
		callMeta.ParentName = fn.obj.typeDef.AsObject.OriginalName
	}

	var deps *ModDeps
	if opts.SkipSelfSchema {
		// Only serve the APIs of the deps of this module. This is currently only needed for the special
		// case of the function used to get the definition of the module itself (which can't obviously
		// be served the API its returning the definition of).
		deps = fn.mod.deps
	} else {
		// by default, serve both deps and the module's own API to itself
		depMods := append([]Mod{fn.mod}, fn.mod.deps.mods...)
		var err error
		deps, err = newModDeps(depMods)
		if err != nil {
			return nil, fmt.Errorf("failed to get deps: %w", err)
		}
	}

	err = fn.api.RegisterFunctionCall(callerDigest, deps, fn.mod, callMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to register function call: %w", err)
	}

	ctrOutputDir, err := ctr.Directory(ctx, fn.api.bk, fn.api.services, modMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get function output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx, fn.api.bk, fn.api.services)
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
	if err := json.Unmarshal(outputBytes, &returnValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	returnValue, err = fn.returnType.ConvertFromSDKResult(ctx, returnValue)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return value: %w", err)
	}

	if err := fn.linkDependencyBlobs(ctx, result, returnValue, fn.metadata.ReturnType); err != nil {
		return nil, fmt.Errorf("failed to link dependency blobs: %w", err)
	}

	return returnValue, nil
}

func (fn *UserModFunction) ReturnType() (ModType, error) {
	return fn.returnType, nil
}

func (fn *UserModFunction) ArgType(argName string) (ModType, error) {
	arg, ok := fn.args[gqlArgName(argName)]
	if !ok {
		return nil, fmt.Errorf("failed to find arg %q", argName)
	}
	return arg.modType, nil
}

// If the result of a Function call contains IDs of resources, we need to ensure that the cache entry for the
// Function call is linked for the cache entries of those resources if those entries aren't reproducible.
// Right now, the only unreproducible output are local dir imports, which are represented as blob:// sources.
// linkDependencyBlobs finds all such blob:// sources and adds a cache lease on that blob in the content store
// to the cacheResult of the function call.
//
// If we didn't do this, then it would be possible for Buildkit to prune the content pointed to by the blob://
// source without pruning the function call cache entry. That would result callers being able to evaluate the
// result of a function call but hitting an error about missing content.
func (fn *UserModFunction) linkDependencyBlobs(ctx context.Context, cacheResult *buildkit.Result, value any, typeDef *core.TypeDef) error {
	if value == nil {
		return nil
	}

	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger,
		core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return nil

	case core.TypeDefKindList:
		listValue, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected list value, got %T", value)
		}
		for _, elem := range listValue {
			if err := fn.linkDependencyBlobs(ctx, cacheResult, elem, typeDef.AsList.ElementTypeDef); err != nil {
				return fmt.Errorf("failed to link dependency blobs: %w", err)
			}
		}
		return nil

	case core.TypeDefKindObject:
		if mapValue, ok := value.(map[string]any); ok {
			// This object is not a core type but we still need to check its
			// Fields for any objects that may contain core objects
			for fieldName, fieldValue := range mapValue {
				field, ok := typeDef.AsObject.FieldByName(fieldName)
				if !ok {
					continue
				}
				if err := fn.linkDependencyBlobs(ctx, cacheResult, fieldValue, field.TypeDef); err != nil {
					return fmt.Errorf("failed to link dependency blobs: %w", err)
				}
			}
			return nil
		}

		if pbDefinitioner, ok := value.(core.HasPBDefinitions); ok {
			pbDefs, err := pbDefinitioner.PBDefinitions()
			if err != nil {
				return fmt.Errorf("failed to get pb definitions: %w", err)
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

		// no dependency blobs to handle
		return nil

	case core.TypeDefKindInterface:
		runtimeVal, ok := value.(*interfaceRuntimeValue)
		if !ok {
			return fmt.Errorf("expected interface runtime val, got %T", value)
		}

		// TODO: handle core types too
		userModObj, ok := runtimeVal.UnderlyingType.(*UserModObject)
		if !ok {
			return fmt.Errorf("expected user mod object, got %T", runtimeVal.UnderlyingType)
		}
		return fn.linkDependencyBlobs(ctx, cacheResult, runtimeVal.Value, userModObj.typeDef)

	default:
		return fmt.Errorf("unhandled type def kind %q", typeDef.Kind)
	}
}
