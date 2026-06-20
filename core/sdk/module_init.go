package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type moduleInitializerModule struct {
	mod   *module
	funcs map[string]*core.Function
}

func (sdk *moduleInitializerModule) InitModule(
	ctx context.Context,
	workspace dagql.ObjectResult[*core.Workspace],
	name string,
	path string,
	args map[string]any,
) (inst dagql.ObjectResult[*core.Changeset], err error) {
	fn, ok := sdk.funcs["initModule"]
	if !ok {
		return inst, fmt.Errorf("initModule is not implemented by this SDK")
	}
	return sdk.mod.callInitFunction(ctx, fn, workspace, map[string]any{
		"name": name,
		"path": path,
	}, args)
}

type clientInitializerModule struct {
	mod   *module
	funcs map[string]*core.Function
}

func (sdk *clientInitializerModule) InitClient(
	ctx context.Context,
	workspace dagql.ObjectResult[*core.Workspace],
	path string,
	module string,
	args map[string]any,
) (inst dagql.ObjectResult[*core.Changeset], err error) {
	fn, ok := sdk.funcs["initClient"]
	if !ok {
		return inst, fmt.Errorf("initClient is not implemented by this SDK")
	}
	return sdk.mod.callInitFunction(ctx, fn, workspace, map[string]any{
		"path":   path,
		"module": module,
	}, args)
}

func (sdk *module) callInitFunction(
	ctx context.Context,
	fn *core.Function,
	workspace dagql.ObjectResult[*core.Workspace],
	standardArgs map[string]any,
	sdkArgs map[string]any,
) (inst dagql.ObjectResult[*core.Changeset], err error) {
	sdkInst, err := sdk.instantiate(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to initialize sdk module %s %s: %w", sdk.mod.Self().Name(), fn.Name, err)
	}

	callArgs, err := sdk.initFunctionCallArgs(ctx, sdkInst.dag, fn, workspace, standardArgs, sdkArgs)
	if err != nil {
		return inst, err
	}

	if err := sdkInst.dag.Select(ctx, sdkInst.sdk, &inst, dagql.Selector{
		Field: fn.Name,
		Args:  callArgs,
	}); err != nil {
		return inst, fmt.Errorf("failed to call sdk module %s: %w", fn.Name, err)
	}
	return inst, nil
}

func (sdk *module) initFunctionCallArgs(
	ctx context.Context,
	dag *dagql.Server,
	fn *core.Function,
	workspace dagql.ObjectResult[*core.Workspace],
	standardArgs map[string]any,
	sdkArgs map[string]any,
) ([]dagql.NamedInput, error) {
	fieldSpec, err := fn.FieldSpec(ctx, core.NewUserMod(sdk.mod))
	if err != nil {
		return nil, fmt.Errorf("inspect sdk %s args: %w", fn.Name, err)
	}
	inputs := fieldSpec.Args.Inputs(dag.View)
	inputByName := make(map[string]dagql.InputSpec, len(inputs))
	for _, input := range inputs {
		inputByName[input.Name] = input
	}

	workspaceID, err := workspace.ID()
	if err != nil {
		return nil, fmt.Errorf("workspace id: %w", err)
	}

	usedSDKArgs := map[string]struct{}{}
	named := make([]dagql.NamedInput, 0, len(inputs))
	for _, argRes := range fn.Args {
		arg := argRes.Self()
		input, ok := inputByName[arg.Name]
		if !ok {
			return nil, fmt.Errorf("inspect sdk %s args: input %q not found", fn.Name, arg.Name)
		}

		if arg.IsWorkspace() {
			named = append(named, dagql.NamedInput{
				Name:  input.Name,
				Value: dagql.NewID[*core.Workspace](workspaceID),
			})
			continue
		}

		if val, ok := standardArgs[arg.Name]; ok {
			decoded, err := input.Type.Decoder().DecodeInput(val)
			if err != nil {
				return nil, fmt.Errorf("decode standard sdk %s arg %q: %w", fn.Name, arg.Name, err)
			}
			named = append(named, dagql.NamedInput{Name: input.Name, Value: decoded})
			continue
		}

		raw, ok := sdkArgs[arg.Name]
		if !ok {
			continue
		}
		decoded, err := input.Type.Decoder().DecodeInput(raw)
		if err != nil {
			return nil, fmt.Errorf("decode sdk %s arg %q: %w", fn.Name, arg.Name, err)
		}
		named = append(named, dagql.NamedInput{Name: input.Name, Value: decoded})
		usedSDKArgs[arg.Name] = struct{}{}
	}

	var unknown []string
	for key := range sdkArgs {
		if _, ok := usedSDKArgs[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown sdk %s arg(s): %v", fn.Name, unknown)
	}

	return named, nil
}

func DecodeInitArgs(raw core.JSON) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw.Bytes()))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("decode sdk init args: %w", err)
	}
	return args, nil
}
