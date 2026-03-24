package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type dangSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

func (sdk *dangSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return nil, false
}

func (sdk *dangSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) RequiredClientGenerationFiles(_ context.Context) (dagql.Array[dagql.String], error) {
	return dagql.NewStringArray(), nil
}

func (sdk *dangSDK) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaJSONFile dagql.Result[*core.File],
	outputDir string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	return inst, fmt.Errorf("dang SDK does not have a client to generate")
}

func (sdk *dangSDK) Codegen(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	return &core.GeneratedCode{
		// no-op
		Code: source.Self().ContextDirectory,
	}, nil
}

func (sdk *dangSDK) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (core.ModuleRuntime, error) {
	return &DangRuntime{
		root:      sdk.root,
		modSource: source,
	}, nil
}

// DangRuntime is a native Dang runtime that doesn't use containers
type DangRuntime struct {
	root      *core.Query
	modSource dagql.ObjectResult[*core.ModuleSource]
}

func (r *DangRuntime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	// Dang runtime doesn't use containers
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *DangRuntime) Call(
	ctx context.Context,
	execMD *buildkit.ExecutionMetadata,
	fnCall *core.FunctionCall,
) (res []byte, clientID string, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = convertError(rerr)
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, "", err
	}

	execMD.CallerClientID = clientMetadata.ClientID
	execMD.SessionID = clientMetadata.SessionID
	execMD.LockMode = clientMetadata.LockMode
	execMD.AllowedLLMModules = clientMetadata.AllowedLLMModules

	if execMD.CallID == nil {
		execMD.CallID = dagql.CurrentID(ctx)
	}
	if execMD.ExecID == "" {
		execMD.ExecID = identity.NewID()
	}
	if execMD.SecretToken == "" {
		execMD.SecretToken = identity.NewID()
	}
	execMD.ClientStableID = identity.NewID()
	if execMD.EncodedModuleID == "" {
		mod := fnCall.Module
		if mod.ResultID == nil {
			return nil, "", fmt.Errorf("current module has no instance ID")
		}
		execMD.EncodedModuleID, err = mod.ResultID.Encode()
		if err != nil {
			return nil, "", err
		}
	}

	if execMD.HostAliases == nil {
		execMD.HostAliases = make(map[string][]string)
	}

	// Get schema introspection file for the op's serialized state.
	schemaJSONFile, err := fnCall.Module.Deps.SchemaIntrospectionJSONFile(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("get schema introspection: %w", err)
	}

	// All calls (init and function calls) go through DangEvalOp for
	// persistent caching through buildkit. On cache hit, the Dang
	// evaluation is skipped entirely.
	callID := dagql.CurrentID(ctx)
	outputBytes, err := solveDangEval(ctx, callID, execMD.CacheMixin, r.modSource, schemaJSONFile, execMD, fnCall)
	if err != nil {
		return nil, "", err
	}
	return outputBytes, execMD.ClientID, nil
}

func convertError(rerr error) *core.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		dagErr := core.NewError(gqlErr.Message)
		if gqlErr.Extensions != nil {
			keys := make([]string, 0, len(gqlErr.Extensions))
			for k := range gqlErr.Extensions {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val, err := json.Marshal(gqlErr.Extensions[k])
				if err != nil {
					fmt.Println("failed to marshal error value:", err)
				}
				dagErr = dagErr.WithValue(k, core.JSON(val))
			}
		}
		return dagErr
	}
	return core.NewError(rerr.Error())
}
