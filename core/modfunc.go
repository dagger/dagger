package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	bksolver "github.com/moby/buildkit/solver"
	solvererror "github.com/moby/buildkit/solver/errdefs"
	llberror "github.com/moby/buildkit/solver/llbsolver/errdefs"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
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
	ParentTyped    dagql.Typed
	ParentFields   map[string]any
	Cache          bool
	SkipSelfSchema bool
	Server         *dagql.Server
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

	// Load contextual arguments
	for _, arg := range fn.metadata.Args {
		name := arg.OriginalName

		// Skip contextual arguments if already set.
		if hasArg[name] || arg.DefaultPath == "" {
			continue
		}

		// Load contextual argument value.
		ctxVal, err := fn.loadContextualArg(ctx, opts.Server, arg)
		if err != nil {
			return nil, fmt.Errorf("failed to load contextual arg %q: %w", arg.Name, err)
		}

		callInputs = append(callInputs, &FunctionCallArgValue{
			Name:  name,
			Value: ctxVal,
		})
	}

	return callInputs, nil
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

	execMD := buildkit.ExecutionMetadata{
		ClientID:        identity.NewID(),
		CallID:          dagql.CurrentID(ctx),
		ExecID:          identity.NewID(),
		CachePerSession: !opts.Cache,
		CacheByCall:     true, // scope the cache key to the function arguments+receiver values
		Internal:        true,
	}

	if opts.ParentTyped != nil {
		// collect any client resources stored in parent fields (secrets/sockets/etc.) and grant
		// this function client access
		parentModType, ok, err := mod.ModTypeFor(ctx, &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(fn.objDef),
		}, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for parent: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for parent %q", fn.objDef.Name)
		}
		execMD.ParentIDs = map[digest.Digest]*resource.ID{}
		if err := parentModType.CollectCoreIDs(ctx, opts.ParentTyped, execMD.ParentIDs); err != nil {
			return nil, fmt.Errorf("failed to collect IDs from parent fields: %w", err)
		}
	}

	execMD.EncodedModuleID, err = mod.InstanceID.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode module ID: %w", err)
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

	ctr := fn.runtime

	metaDir, err := NewScratchDirectory(ctx, mod.Query, mod.Query.Platform())
	if err != nil {
		return nil, fmt.Errorf("failed to create mod metadata directory: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, ContainerExecOpts{
		ExperimentalPrivilegedNesting: true,
		NestedExecMetadata:            &execMD,
		UseEntrypoint:                 true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	bk, err := fn.root.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	_, err = ctr.Evaluate(ctx)
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
			return nil, errors.New(errInst.Self.Message)
		}
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
	clientID, err := ctr.usedClientID(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get used client id")
	}

	// If the function returned anything that's isolated per-client, this caller client should
	// have access to it now since it was returned to them (i.e. secrets/sockets/etc).
	returnedIDs := map[digest.Digest]*resource.ID{}
	if err := fn.returnType.CollectCoreIDs(ctx, returnValueTyped, returnedIDs); err != nil {
		return nil, fmt.Errorf("failed to collect IDs: %w", err)
	}

	for _, id := range returnedIDs {
		if err := fn.root.AddClientResourcesFromID(ctx, id, clientID, false); err != nil {
			return nil, fmt.Errorf("failed to add client resources from ID: %w", err)
		}
	}

	// NOTE: once generalized function caching is enabled we need to ensure that any non-reproducible
	// cache entries are linked to the result of this call.
	// See the previous implementation of this for a reference:
	// https://github.com/dagger/dagger/blob/7c31db76e07c9a17fcdb3f3c4513c915344c1da8/core/modfunc.go#L483

	return returnValueTyped, nil
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

	var opErr *solvererror.OpError
	if !errors.As(baseErr, &opErr) {
		return id, false, nil
	}
	op := opErr.Op
	if op == nil || op.Op == nil {
		return id, false, nil
	}
	execOp, ok := op.Op.(*bksolverpb.Op_Exec)
	if !ok {
		return id, false, nil
	}

	// This was an exec error, we will retrieve the exec's output and include
	// it in the error message

	// get the mnt containing module response data (in this case, the error ID)
	var metaMountResult bksolver.Result
	var foundMounts []string
	for i, mnt := range execOp.Exec.Mounts {
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
		local := source.AsLocalSource.Value
		props[prefix+"source_kind"] = "local"
		props[prefix+"local_subpath"] = local.RootSubpath
	case ModuleSourceKindGit:
		git := source.AsGitSource.Value
		props[prefix+"source_kind"] = "git"
		props[prefix+"git_symbolic"] = git.Symbolic()
		props[prefix+"git_clone_url"] = git.CloneRef // todo(guillaume): remove as deprecated
		props[prefix+"git_clone_ref"] = git.CloneRef
		props[prefix+"git_subpath"] = git.RootSubpath
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
func (fn *ModuleFunction) loadContextualArg(ctx context.Context, dag *dagql.Server, arg *FunctionArg) (JSON, error) {
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

		dirID, err := dir.ID().Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to encode dir ID: %w", err)
		}

		return JSON(fmt.Sprintf(`"%s"`, dirID)), nil
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

		encodedFileID, err := fileID.Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to encode file ID: %w", err)
		}

		return JSON(fmt.Sprintf(`"%s"`, encodedFileID)), nil
	default:
		return nil, fmt.Errorf("unknown contextual argument type %q", arg.TypeDef.AsObject.Value.Name)
	}
}
