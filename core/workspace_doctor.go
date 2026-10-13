package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// ValidateWorkspaceModuleSettings checks explicitly configured constructor
// settings without calling the module's constructor. Object settings use the
// same address resolver as calls, including sibling workspace module wiring.
func ValidateWorkspaceModuleSettings(ctx context.Context, mod dagql.ObjectResult[*Module]) []error {
	settings := mod.Self().WorkspaceConfig
	if len(settings) == 0 {
		return nil
	}
	obj, ok := mod.Self().MainObject()
	var constructor *Function
	if ok && obj.Constructor.Valid {
		constructor = obj.Constructor.Value.Self()
	}
	// Only the metadata and module identity are needed by the default resolver.
	fn := &ModuleFunction{mod: mod, objDef: obj, metadata: constructor}
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var errs []error
	for _, key := range keys {
		var arg *FunctionArg
		if constructor != nil {
			for _, candidate := range constructor.Args {
				a := candidate.Self()
				if strings.EqualFold(key, a.Name) || strings.EqualFold(key, a.OriginalName) {
					arg = a
					break
				}
			}
		}
		if arg == nil {
			errs = append(errs, fmt.Errorf("unknown setting %q", key))
			continue
		}
		if err := validateWorkspaceSetting(ctx, fn, arg, settings[key]); err != nil {
			errs = append(errs, fmt.Errorf("setting %q: %w", key, err))
		}
	}
	return errs
}

func validateWorkspaceSetting(ctx context.Context, fn *ModuleFunction, arg *FunctionArg, setting any) error {
	var value *FunctionCallArgValue
	var err error
	// Enum/scalar defaults have already been encoded by
	// ApplyWorkspaceDefaultsToTypeDefs, including bare string values.
	if (arg.TypeDef.Self().Kind == TypeDefKindEnum || arg.TypeDef.Self().Kind == TypeDefKindScalar) && arg.DefaultValue != nil {
		value = &FunctionCallArgValue{Value: arg.DefaultValue}
	} else {
		value, err = fn.newUserDefault(arg, configValueToString(setting)).CallInput(ctx)
		if err != nil {
			return err
		}
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value.Value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	input, err := arg.TypeDef.Self().ToInput().Decoder().DecodeInput(decoded)
	if err != nil {
		return err
	}
	modType, found, err := NewUserMod(fn.mod).ModTypeFor(ctx, arg.TypeDef.Self(), true)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("setting type not found")
	}
	_, err = modType.ConvertToSDKInput(ctx, input)
	return err
}
