package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	bksolver "github.com/moby/buildkit/solver"
	llberror "github.com/moby/buildkit/solver/llbsolver/errdefs"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleFunction struct {
	mod     *Module
	objDef  *ObjectTypeDef // may be nil for special functions like the module definition function call
	runtime dagql.Instance[*Container]

	metadata   *Function
	returnType ModType
	args       map[string]*UserModFunctionArg
}

type UserModFunctionArg struct {
	metadata *FunctionArg
	modType  ModType
}

func NewModFunction(
	ctx context.Context,
	mod *Module,
	objDef *ObjectTypeDef,
	runtime dagql.Instance[*Container],
	metadata *Function,
) (*ModuleFunction, error) {
	returnType, ok, _, err := mod.ModTypeFor(ctx, metadata.ReturnType, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find mod type for function %q return type: %q", metadata.Name, metadata.ReturnType.ToType())
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadata := range metadata.Args {
		argModType, ok, _, err := mod.ModTypeFor(ctx, argMetadata.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for function %q arg %q type: %w", metadata.Name, argMetadata.Name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for function %q arg %q type: %q", metadata.Name, argMetadata.Name, argMetadata.TypeDef.ToType())
		}
		argTypes[argMetadata.Name] = &UserModFunctionArg{
			metadata: argMetadata,
			modType:  argModType,
		}
	}

	return &ModuleFunction{
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
	ParentTyped    dagql.Typed
	ParentFields   map[string]any
	Cache          bool
	SkipSelfSchema bool
	Server         *dagql.Server

	// If true, don't mix in the digest for the current dagql call into the cache key for
	// the exec-op underlying the function call.
	//
	// We want the function call to be cached by the dagql digest in almost every case
	// since the current dagql call is typically the actual function call. However, in
	// some corner cases we may calling a function internally within a separate dagql
	// call and don't want the current call digest mixed in, e.g. during the special
	// function call that retrieves the module typedefs.
	SkipCallDigestCacheKey bool
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
	query, err := CurrentQuery(ctx)
	if err != nil {
		slog.Error("failed to get current query for module call analytics", "err", err)
		return
	}
	if caller, err := query.CurrentModule(ctx); err == nil {
		props["caller_type"] = "module"
		moduleAnalyticsProps(caller, "caller_", props)
	} else if dagql.IsInternal(ctx) {
		props["caller_type"] = "internal"
	} else {
		props["caller_type"] = "direct"
	}
	analytics.Ctx(ctx).Capture(ctx, "module_call", props)
}

// setCallInputs sets the call inputs for the function call.
//
// It first load the argument set by the user.
// Then the default values.
// Finally the contextual arguments.
func (fn *ModuleFunction) setCallInputs(ctx context.Context, opts *CallOpts) ([]*FunctionCallArgValue, error) {
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

		if len(arg.metadata.Ignore) > 0 && arg.metadata.DefaultPath == "" { // DefaultPath (aka contextual) args already have ignore applied
			converted, err = fn.applyIgnoreOnDir(ctx, opts.Server, arg.metadata, converted)
			if err != nil {
				return nil, fmt.Errorf("failed to apply ignore pattern on arg %q: %w", input.Name, err)
			}
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

	// Load default value
	for _, arg := range fn.metadata.Args {
		name := arg.OriginalName
		if hasArg[name] || arg.DefaultValue == nil {
			continue
		}
		callInputs = append(callInputs, &FunctionCallArgValue{
			Name:  name,
			Value: arg.DefaultValue,
		})

		hasArg[name] = true
	}

	return callInputs, nil
}

func (fn *ModuleFunction) CacheConfigForCall(
	ctx context.Context,
	parent dagql.Object,
	args map[string]dagql.Input,
	view dagql.View,
	inputCfg dagql.CacheConfig,
) (*dagql.CacheConfig, error) {
	cacheCfg, err := fn.mod.CacheConfigForCall(ctx, parent, args, view, inputCfg)
	if err != nil {
		return nil, err
	}

	dgstInputs := []string{cacheCfg.Digest.String()}

	var ctxArgs []*FunctionArg
	for _, argMetadata := range fn.metadata.Args {
		if argMetadata.DefaultPath == "" {
			// not a contextual argument
			continue
		}
		if args[argMetadata.Name] != nil {
			// was explicitly set by the user, no need to load default contextual value
			continue
		}
		ctxArgs = append(ctxArgs, argMetadata)
	}
	if len(ctxArgs) > 0 {
		cacheCfg.UpdatedArgs = make(map[string]dagql.Input)
		var mu sync.Mutex
		type argInput struct {
			name string
			val  dagql.IDType
		}
		ctxArgVals := make([]*argInput, len(ctxArgs))
		eg, ctx := errgroup.WithContext(ctx)
		srv := dagql.CurrentDagqlServer(ctx)
		for i, arg := range ctxArgs {
			eg.Go(func() error {
				ctxVal, err := fn.loadContextualArg(ctx, srv, arg)
				if err != nil {
					return fmt.Errorf("failed to load contextual arg %q: %w", arg.Name, err)
				}

				ctxArgVals[i] = &argInput{
					name: arg.OriginalName,
					val:  ctxVal,
				}
				mu.Lock()
				cacheCfg.UpdatedArgs[arg.Name] = dagql.Opt(ctxVal)
				mu.Unlock()

				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}

		for _, arg := range ctxArgVals {
			dgstInputs = append(dgstInputs, arg.name, arg.val.ID().Digest().String())
		}
	}

	cacheCfg.Digest = dagql.HashFrom(dgstInputs...)
	return cacheCfg, nil
}

func (fn *ModuleFunction) Call(ctx context.Context, opts *CallOpts) (t dagql.Typed, rerr error) { //nolint: gocyclo
	mod := fn.mod

	lg := bklog.G(ctx).WithField("module", mod.Name()).WithField("function", fn.metadata.Name)
	if fn.objDef != nil {
		lg = lg.WithField("object", fn.objDef.Name)
	}
	ctx = bklog.WithLogger(ctx, lg)

	// Capture analytics for the function call.
	// Calls without function name are internal and excluded.
	fn.recordCall(ctx)

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	execMD := buildkit.ExecutionMetadata{
		ClientID:          identity.NewID(),
		CallID:            dagql.CurrentID(ctx),
		ExecID:            identity.NewID(),
		Internal:          true,
		ParentIDs:         map[digest.Digest]*resource.ID{},
		AllowedLLMModules: clientMetadata.AllowedLLMModules,
	}

	var cacheMixins []string
	if !opts.Cache {
		// Scope the exec cache key to the current session ID. It will be
		// cached in the context of the session but invalidated across
		// different sessions.
		cacheMixins = append(cacheMixins, clientMetadata.SessionID)
	}
	if !opts.SkipCallDigestCacheKey {
		// If true, scope the exec cache key to the current dagql call digest. This is needed currently
		// for module function calls specifically so that their cache key is based on their arguments and
		// receiver object.
		cacheMixins = append(cacheMixins, dagql.CurrentID(ctx).Digest().String())
	}
	execMD.CacheMixin = dagql.HashFrom(cacheMixins...)

	callInputs, err := fn.setCallInputs(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to set call inputs: %w", err)
	}

	bklog.G(ctx).Debug("function call")
	defer func() {
		bklog.G(ctx).Debug("function call done")
		if rerr != nil {
			bklog.G(ctx).WithError(rerr).Error("function call errored")
		}
	}()

	parentJSON, err := json.Marshal(opts.ParentFields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal parent value: %w", err)
	}

	if opts.ParentTyped != nil {
		// collect any client resources stored in parent fields (secrets/sockets/etc.) and grant
		// this function client access
		parentModType, ok, _, err := mod.ModTypeFor(ctx, &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(fn.objDef),
		}, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for parent: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for parent %q", fn.objDef.Name)
		}
		if err := parentModType.CollectCoreIDs(ctx, opts.ParentTyped, execMD.ParentIDs); err != nil {
			return nil, fmt.Errorf("failed to collect IDs from parent fields: %w", err)
		}
	}

	if mod.InstanceID != nil {
		execMD.EncodedModuleID, err = mod.InstanceID.Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to encode module ID: %w", err)
		}
	}

	fnCall := &FunctionCall{
		Name:      fn.metadata.OriginalName,
		Parent:    parentJSON,
		InputArgs: callInputs,
	}
	if fn.objDef != nil {
		fnCall.ParentName = fn.objDef.OriginalName
	}
	execMD.EncodedFunctionCall, err = json.Marshal(fnCall)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal function call: %w", err)
	}

	srv := dagql.CurrentDagqlServer(ctx)

	var metaDir dagql.Instance[*Directory]
	err = srv.Select(ctx, srv.Root(), &metaDir,
		dagql.Selector{
			Field: "directory",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mod metadata directory: %w", err)
	}

	var ctr dagql.Instance[*Container]
	err = srv.Select(ctx, fn.runtime, &ctr,
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modMetaDirPath)},
				{Name: "source", Value: dagql.NewID[*Directory](metaDir.ID())},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	execCtx := ctx
	execCtx = dagql.WithSkip(execCtx) // this span shouldn't be shown (it's entirely useless)
	err = srv.Select(execCtx, ctr, &ctr,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewSerializedString(&execMD)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	_, err = ctr.Self.Evaluate(ctx)
	if err != nil {
		id, ok, extractErr := extractError(ctx, bk, err)
		if extractErr != nil {
			// if the module hasn't provided us with a nice error, just return the
			// original error
			return nil, err
		}
		if ok {
			errInst, err := id.Load(ctx, opts.Server)
			if err != nil {
				return nil, fmt.Errorf("failed to load error instance: %w", err)
			}
			dagErr := errInst.Self.Clone()
			originCtx := trace.SpanContextFromContext(
				telemetry.Propagator.Extract(
					context.Background(),
					telemetry.AnyMapCarrier(dagErr.Extensions()),
				),
			)
			if !originCtx.IsValid() {
				// If the Error doesn't already have an origin, inject the current trace
				// context as its origin.
				tm := propagation.MapCarrier{}
				telemetry.Propagator.Inject(ctx, tm)
				for _, key := range tm.Keys() {
					val := tm.Get(key)
					valJSON, err := json.Marshal(val)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal value: %w", err)
					}
					dagErr.Values = append(dagErr.Values, &ErrorValue{
						Name:  key,
						Value: JSON(valJSON),
					})
				}
			}
			return nil, dagErr
		}
		if fn.metadata.OriginalName == "" {
			return nil, fmt.Errorf("call constructor: %w", err)
		} else {
			return nil, fmt.Errorf("call function %q: %w", fn.metadata.OriginalName, err)
		}
	}

	ctrOutputDir, err := ctr.Self.Directory(ctx, modMetaDirPath)
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
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	returnValueTyped, err := fn.returnType.ConvertFromSDKResult(ctx, returnValue)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return value: %w", err)
	}

	// Get the client ID actually used during the function call - this might not
	// be the same as execMD.ClientID if the function call was cached at the
	// buildkit level
	clientID, err := ctr.Self.usedClientID(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get used client id")
	}

	// If the function returned anything that's isolated per-client, this caller client should
	// have access to it now since it was returned to them (i.e. secrets/sockets/etc).
	returnedIDs := map[digest.Digest]*resource.ID{}
	if err := fn.returnType.CollectCoreIDs(ctx, returnValueTyped, returnedIDs); err != nil {
		return nil, fmt.Errorf("failed to collect IDs: %w", err)
	}

	// NOTE: once generalized function caching is enabled we need to ensure that any non-reproducible
	// cache entries are linked to the result of this call.
	// See the previous implementation of this for a reference:
	// https://github.com/dagger/dagger/blob/7c31db76e07c9a17fcdb3f3c4513c915344c1da8/core/modfunc.go#L483

	// Function calls are cached per-session, but every client caller needs to add
	// secret/socket/etc. resources from the result to their store.
	returnedIDsList := make([]*resource.ID, 0, len(returnedIDs))
	for _, id := range returnedIDs {
		returnedIDsList = append(returnedIDsList, id)
	}
	secretTransferPostCall, err := ResourceTransferPostCall(ctx, query, clientID, returnedIDsList...)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret transfer post call: %w", err)
	}
	return dagql.NewPostCallTyped(returnValueTyped, secretTransferPostCall), nil
}

func extractError(ctx context.Context, client *buildkit.Client, baseErr error) (dagql.ID[*Error], bool, error) {
	var id dagql.ID[*Error]

	var execErr *llberror.ExecError
	if errors.As(baseErr, &execErr) {
		defer func() {
			execErr.Release()
			execErr.OwnerBorrowed = true
		}()
	}

	var ierr buildkit.RichError
	if !errors.As(baseErr, &ierr) {
		return id, false, nil
	}

	// get the mnt containing module response data (in this case, the error ID)
	var metaMountResult bksolver.Result
	var foundMounts []string
	for i, mnt := range ierr.Mounts {
		foundMounts = append(foundMounts, mnt.Dest)
		if mnt.Dest == modMetaDirPath {
			metaMountResult = execErr.Mounts[i]
			break
		}
	}
	if metaMountResult == nil {
		slog.Warn("failed to find meta mount", "mounts", foundMounts, "want", modMetaDirPath)
		return id, false, nil
	}

	workerRef, ok := metaMountResult.Sys().(*bkworker.WorkerRef)
	if !ok {
		return id, false, errors.Join(baseErr, fmt.Errorf("invalid ref type: %T", metaMountResult.Sys()))
	}
	mntable, err := workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(client.ID()))
	if err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	idBytes, err := buildkit.ReadSnapshotPath(ctx, client, mntable, modMetaErrorPath)
	if err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	if err := id.Decode(string(idBytes)); err != nil {
		return id, false, errors.Join(err, baseErr)
	}

	return id, true, nil
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
		props[prefix+"source_kind"] = "local"
		props[prefix+"local_subpath"] = source.SourceRootSubpath
	case ModuleSourceKindGit:
		git := source.Git
		props[prefix+"source_kind"] = "git"
		props[prefix+"git_symbolic"] = git.Symbolic
		props[prefix+"git_clone_url"] = git.CloneRef // todo(guillaume): remove as deprecated
		props[prefix+"git_clone_ref"] = git.CloneRef
		props[prefix+"git_subpath"] = source.SourceRootSubpath
		props[prefix+"git_version"] = git.Version
		props[prefix+"git_commit"] = git.Commit
		props[prefix+"git_html_repo_url"] = git.HTMLRepoURL
	}
}

// loadContextualArg loads a contextual argument from the module context directory.
//
// For Directory, it will load the directory from the module context directory.
// For file, it will loa the directory containing the file and then query the file ID from this directory.
//
// This functions returns the ID of the loaded object.
func (fn *ModuleFunction) loadContextualArg(
	ctx context.Context,
	dag *dagql.Server,
	arg *FunctionArg,
) (dagql.IDType, error) {
	if arg.TypeDef.Kind != TypeDefKindObject {
		return nil, fmt.Errorf("contextual argument %q must be a Directory or a File", arg.OriginalName)
	}

	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required for contextual argument %q", arg.OriginalName)
	}

	switch arg.TypeDef.AsObject.Value.Name {
	case "Directory":
		slog.Debug("moduleFunction.loadContextualArg: loading contextual directory", "fn", arg.Name, "dir", arg.DefaultPath)

		dir, err := fn.mod.Source.Self.LoadContext(ctx, dag, arg.DefaultPath, arg.Ignore)
		if err != nil {
			return nil, fmt.Errorf("failed to load contextual directory %q: %w", arg.DefaultPath, err)
		}
		return dagql.NewID[*Directory](dir.ID()), nil

	case "File":
		slog.Debug("moduleFunction.loadContextualArg: loading contextual file", "fn", arg.Name, "file", arg.DefaultPath)

		// We first load the directory from the context path, then we load the file from the path relative to the directory.
		dirPath := filepath.Dir(arg.DefaultPath)
		filePath := filepath.Base(arg.DefaultPath)

		// Load the directory containing the file.
		dir, err := fn.mod.Source.Self.LoadContext(ctx, dag, dirPath, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to load contextual directory %q: %w", dirPath, err)
		}

		var fileID FileID

		// We need to load the fileID from the directory itself, because `*File` doesn't have a `ID` field,
		// we use select instead.
		err = dag.Select(ctx, dir, &fileID,
			dagql.Selector{
				Field: "file",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(filePath)},
				},
			},
			dagql.Selector{
				Field: "id",
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load contextual file %q: %w", filePath, err)
		}

		return fileID, nil

	default:
		return nil, fmt.Errorf("unknown contextual argument type %q", arg.TypeDef.AsObject.Value.Name)
	}
}

func (fn *ModuleFunction) applyIgnoreOnDir(ctx context.Context, dag *dagql.Server, arg *FunctionArg, value any) (any, error) {
	if arg.TypeDef.Kind != TypeDefKindObject || arg.TypeDef.AsObject.Value.Name != "Directory" {
		return nil, fmt.Errorf("argument %q must be of type Directory to apply ignore pattern: [%s]", arg.OriginalName, strings.Join(arg.Ignore, ","))
	}

	if dag == nil {
		return nil, fmt.Errorf("dagql server is nil but required to ignore pattern on directory %q", arg.OriginalName)
	}

	applyIgnore := func(dir dagql.IDable) (JSON, error) {
		var ignoredDir dagql.Instance[*Directory]

		err := dag.Select(ctx, dag.Root(), &ignoredDir,
			dagql.Selector{
				Field: "directory",
			},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "directory", Value: dagql.NewID[*Directory](dir.ID())},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(arg.Ignore...))},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to apply ignore pattern on directory %q: %w", arg.OriginalName, err)
		}

		dirID, err := ignoredDir.ID().Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to apply ignore pattern on directory %q: %w", arg.Name, err)
		}

		return JSON(dirID), nil
	}

	switch value := value.(type) {
	case DynamicID:
		return applyIgnore(value)
	case dagql.ID[*Directory]:
		return applyIgnore(value)
	default:
		return nil, fmt.Errorf("argument %q must be of type Directory to apply ignore pattern ([%s]) but type is %#v", arg.OriginalName, strings.Join(arg.Ignore, ", "), value)
	}
}
