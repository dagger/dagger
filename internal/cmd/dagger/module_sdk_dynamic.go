package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// moduleInitSDKArgName is the SDK named on the current module init command
// line. Only that SDK contributes setting flags to module init.
var moduleInitSDKArgName string

// moduleInitSDKArg returns the SDK named on a `dagger module init <SDK>`
// command line, or "" when the command is not module init or names no SDK.
//
// It reads the output of parseGlobalFlags, which has already removed flags and
// their values, so the first remaining token after init is the SDK.
func moduleInitSDKArg(args []string) string {
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
	}
	if len(args) < 3 || (args[0] != "module" && args[0] != "mod") || args[1] != "init" {
		return ""
	}
	for _, arg := range args[2:] {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func moduleSDKSettingsRegistrationArgs(args, rawArgs []string) bool {
	if len(args) == 0 {
		return false
	}
	help := false
	if args[0] == "help" {
		help = true
		args = args[1:]
	}
	if len(args) < 2 || (args[0] != "module" && args[0] != "mod") {
		return false
	}
	cmd := moduleInitCmd
	if args[1] == "client" {
		if len(args) < 3 || args[2] != "add" {
			return false
		}
		cmd = moduleClientAddCmd
	} else if args[1] != "init" {
		return false
	}
	for _, arg := range rawArgs {
		if arg == "--help" || arg == "-h" {
			help = true
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		if before, _, found := strings.Cut(name, "="); found {
			name = before
		}
		if cmd.Flags().Lookup(name) == nil && rootCmd.PersistentFlags().Lookup(name) == nil {
			return true
		}
	}
	return help
}

func registerModuleSDKSettingFlags(ctx context.Context, initSDK string) error {
	moduleInitSDKArgName = initSDK
	cfg, cfgPath, err := readWorkspaceConfigForSDKInitRegistration()
	if err != nil {
		return err
	}
	if cfg == nil && isObviouslyRemoteWorkspaceRef(workspaceRef) {
		return withEngineSilent(ctx, client.Params{
			SkipWorkspaceModules:           true,
			SuppressCompatWorkspaceWarning: true,
		}, func(ctx context.Context, ec *client.Client) error {
			cfg, cfgPath, err := readSelectedWorkspaceConfig(ctx, ec.Dagger())
			if err != nil {
				return err
			}
			return registerModuleSDKSettingFlagsFromConfig(ctx, cfg, cfgPath, ec.Dagger())
		})
	}
	if cfg == nil {
		return nil
	}
	return withEngineSilent(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return registerModuleSDKSettingFlagsFromConfig(ctx, cfg, cfgPath, ec.Dagger())
	})
}

func registerModuleSDKSettingFlagsFromConfig(
	ctx context.Context,
	cfg *workspace.Config,
	cfgPath string,
	dag *dagger.Client,
) error {
	if cfg == nil {
		return nil
	}
	sdks, err := configuredSDKs(cfg)
	if err != nil {
		return err
	}
	cfgDir := filepath.Dir(cfgPath)
	for _, sdk := range sdks {
		sdkRef, err := sdkInitModuleEntrySource(sdk.entry, cfgDir)
		if err != nil {
			return err
		}
		modSrc := dag.ModuleSource(sdkRef)
		if workspace.IsLocalRef(sdk.entry.Source, sdk.entry.Pin) {
			currentWorkspace := dag.CurrentWorkspace().Reloaded()
			workspaceConfigFile, err := currentWorkspace.ConfigFile(ctx)
			if err != nil {
				return fmt.Errorf("find SDK module %q workspace config: %w", sdk.commandName, err)
			}
			if workspaceConfigFile == "" {
				return fmt.Errorf("find SDK module %q workspace config: no active config file", sdk.commandName)
			}
			workspacePath := filepath.Join(filepath.Dir(workspaceConfigFile), sdk.entry.Source)
			modSrc = currentWorkspace.ModuleSource(filepath.ToSlash(workspacePath))
		}

		args, err := inspectSDKModuleConstructorArgs(ctx, dag, sdkRef, modSrc)
		if err != nil {
			return err
		}
		// module init names its SDK positionally, so its setting flags drop the
		// SDK prefix and only the named SDK contributes them. module client add
		// selects its SDK later, so it keeps every SDK's prefixed flags.
		if sdk.commandName == moduleInitSDKArgName {
			if err := addSDKModuleSettingFlags(moduleInitCmd, sdk, args, false); err != nil {
				return err
			}
		}
		if err := addSDKModuleSettingFlags(moduleClientAddCmd, sdk, args, true); err != nil {
			return err
		}
	}
	return nil
}

func inspectSDKModuleConstructorArgs(
	ctx context.Context,
	dag *dagger.Client,
	sdkRef string,
	modSrc *dagger.ModuleSource,
) ([]*modFunctionArg, error) {
	mod, err := initializeModule(ctx, dag, sdkRef, modSrc, initModuleOpts{skipDependencies: true})
	if err != nil {
		return nil, fmt.Errorf("inspect SDK module %q: %w", sdkRef, err)
	}
	constructor := mod.ModuleConstructor()
	if constructor == nil {
		return nil, nil
	}
	if err := mod.LoadFunctionTypeDefs(constructor); err != nil {
		return nil, fmt.Errorf("inspect SDK module %q settings: %w", sdkRef, err)
	}
	return constructor.Args, nil
}

func addSDKModuleSettingFlags(cmd *cobra.Command, sdk configuredSDK, args []*modFunctionArg, prefixed bool) error {
	for _, arg := range args {
		if arg.IsWorkspace() {
			continue
		}
		settingName := arg.Name
		if prefixed {
			settingName = sdk.commandName + "-" + arg.Name
		}
		flagArg := &modFunctionArg{
			Name:         settingName,
			Description:  arg.Description,
			TypeDef:      arg.TypeDef,
			DefaultValue: arg.DefaultValue,
			DefaultPath:  arg.DefaultPath,
			Ignore:       arg.Ignore,
		}
		if configured, ok := sdk.entry.Settings[arg.Name]; ok {
			encoded, err := json.Marshal(configured)
			if err != nil {
				return fmt.Errorf("encode SDK module %q setting %q: %w", sdk.commandName, arg.Name, err)
			}
			flagArg.DefaultValue = dagger.JSON(encoded)
		}
		if err := flagArg.AddFlag(cmd.Flags()); err != nil {
			var unsupported *UnsupportedFlagError
			if errors.As(err, &unsupported) {
				continue
			}
			return err
		}
		flagName := flagArg.FlagName()
		if err := cmd.Flags().SetAnnotation(flagName, sdkModuleSettingAnnotation, []string{sdk.commandName, arg.Name}); err != nil {
			return err
		}
		if err := cmd.Flags().SetAnnotation(flagName, "help:group", []string{"SDK settings"}); err != nil {
			return err
		}
	}
	return nil
}
