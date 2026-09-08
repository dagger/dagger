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

func moduleSDKCommandSelection(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if args[0] == "help" {
		args = args[1:]
	}
	if len(args) < 2 || (args[0] != "module" && args[0] != "mod") {
		return "", false
	}
	if args[1] == "init" {
		if len(args) >= 3 {
			return args[2], true
		}
		return "", true
	}
	if len(args) >= 3 && args[1] == "client" && (args[2] == "add" || args[2] == "rm") {
		// Client commands only need SDK names for flag help. Their module
		// target must not trigger SDK constructor inspection.
		return "", true
	}
	return "", false
}

func registerModuleSDKCommands(ctx context.Context, selectedSDK string) error {
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
			return registerModuleSDKCommandsFromConfig(ctx, cfg, cfgPath, ec.Dagger(), selectedSDK)
		})
	}
	if cfg == nil || selectedSDK == "" {
		return registerModuleSDKCommandsFromConfig(ctx, cfg, cfgPath, nil, "")
	}
	return withEngineSilent(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return registerModuleSDKCommandsFromConfig(ctx, cfg, cfgPath, ec.Dagger(), selectedSDK)
	})
}

func registerModuleSDKCommandsFromConfig(
	ctx context.Context,
	cfg *workspace.Config,
	cfgPath string,
	dag *dagger.Client,
	selectedSDK string,
) error {
	sdks, err := configuredSDKs(cfg)
	if err != nil {
		return err
	}
	registerModuleClientSDKFlagHelp(sdks)
	cfgDir := filepath.Dir(cfgPath)
	for _, sdk := range sdks {
		var args []*modFunctionArg
		if sdk.commandName == selectedSDK {
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

			args, err = inspectSDKModuleConstructorArgs(ctx, dag, sdkRef, modSrc)
			if err != nil {
				return err
			}
		}
		initCmd, err := newSDKModuleInitCommand(sdk, args)
		if err != nil {
			return err
		}
		moduleInitCmd.AddCommand(initCmd)
	}
	return nil
}

func registerModuleClientSDKFlagHelp(sdks []configuredSDK) {
	names := make([]string, 0, len(sdks))
	for _, sdk := range sdks {
		names = append(names, sdk.commandName)
	}
	available := "no SDKs installed"
	if len(names) > 0 {
		available = "available: " + strings.Join(names, ", ")
	}
	moduleClientAddCmd.Flags().Lookup("sdk").Usage = "Select an installed `SDK` (" + available + "; default: deepest scope)"
	moduleClientRemoveCmd.Flags().Lookup("sdk").Usage = "Select an installed `SDK` (" + available + "; default: deepest matching scope)"
}

func newSDKModuleInitCommand(sdk configuredSDK, args []*modFunctionArg) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:                   sdk.commandName,
		Short:                 fmt.Sprintf("Initialize a module with the %s SDK", sdk.commandName),
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSDKModuleInit(cmd, sdk.commandName)
		},
	}
	if err := addSDKModuleSettingFlags(cmd, sdk, args); err != nil {
		return nil, err
	}
	return cmd, nil
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

func addSDKModuleSettingFlags(cmd *cobra.Command, sdk configuredSDK, args []*modFunctionArg) error {
	for _, arg := range args {
		if arg.IsWorkspace() {
			continue
		}
		flagArg := &modFunctionArg{
			Name:         arg.Name,
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
