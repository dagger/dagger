package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// prepareModuleSDKCommands parses the unconsumed suffix after discovering SDK
// settings. A context change permits one more discovery, with the same flags.
func prepareModuleSDKCommands(ctx context.Context, root *cobra.Command, args []string, invocationDir string, register func(context.Context, string) error) error {
	selectedSDK, needed := moduleSDKCommandSelection(args)
	if !needed {
		return nil
	}
	if selectedSDK == "" || args[0] == "help" {
		return register(ctx, selectedSDK)
	}

	cmd, _ := resolveCommand(root, args)
	contextValues := moduleSDKContextValues(root)
	possibleContext := copyCommandFlags(cmd, "SDK context")
	possibleContext.SetInterspersed(true)
	possibleContext.VisitAll(func(flag *pflag.Flag) { flag.Value = ignoredFlagValue{Value: flag.Value} })
	_ = possibleContext.Parse(args[3:])
	var contextFlags []string
	possibleContext.Visit(func(flag *pflag.Flag) {
		if _, ok := contextValues[flag.Name]; ok {
			contextFlags = append(contextFlags, flag.Name)
		}
	})
	ambiguous := func(reason string) error {
		example := []string{root.Name()}
		for _, name := range contextFlags {
			arg := "--" + name
			if root.PersistentFlags().Lookup(name).NoOptDefVal == "" {
				arg += "=<value>"
			}
			example = append(example, arg)
		}
		example = append(example, "module", "init", selectedSDK)
		return fmt.Errorf("SDK %q: %s. Flags after the SDK name can be SDK settings. Put global context flags before SDK selection, for example: %s", selectedSDK, reason, strings.Join(example, " "))
	}
	if err := register(ctx, selectedSDK); err != nil {
		if len(contextFlags) > 0 {
			return fmt.Errorf("%w: %w", ambiguous("cannot load the SDK in the initial workspace"), err)
		}
		return err
	}
	cmd, _ = resolveCommand(root, args)
	if commandName(cmd) != "module init "+selectedSDK {
		if len(contextFlags) > 0 {
			return ambiguous("the SDK is absent from the initial workspace, so trailing context flags cannot be classified")
		}
		return nil // Cobra reports the unknown SDK command.
	}
	settings := moduleSDKSettingTypes(cmd)
	contextFlags = slices.DeleteFunc(contextFlags, func(name string) bool {
		return cmd.Flags().Lookup(name) != root.PersistentFlags().Lookup(name)
	})
	// args contains only the command path and the unconsumed suffix. Prefix
	// counters and repeatable flags must not be applied a second time.
	parseGlobalFlags(root, args)
	if maps.Equal(contextValues, moduleSDKContextValues(root)) {
		return nil
	}
	if workdir != contextValues["workdir"] {
		path := workdir
		if path == "" {
			path = os.Getenv("DAGGER_WORKDIR")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(invocationDir, path)
		}
		resolved, err := NormalizeWorkdir(path)
		if err != nil {
			return err
		}
		if err := os.Chdir(resolved); err != nil {
			return fmt.Errorf("change workdir: %w", err)
		}
		workdir = resolved
	}
	if err := register(ctx, selectedSDK); err != nil {
		return fmt.Errorf("%w: %w", ambiguous("cannot load the SDK with the trailing context flags"), err)
	}
	cmd, _ = resolveCommand(root, args)
	if commandName(cmd) != "module init "+selectedSDK || !maps.Equal(settings, moduleSDKSettingTypes(cmd)) {
		return ambiguous("the trailing context flags select an SDK with different settings")
	}
	return nil
}

func moduleSDKContextValues(root *cobra.Command) map[string]string {
	values := map[string]string{}
	for _, name := range []string{"workdir", "workspace", "env", "engine", "cloud"} {
		if flag := root.PersistentFlags().Lookup(name); flag != nil {
			values[name] = flag.Value.String()
		}
	}
	return values
}

func moduleSDKSettingTypes(cmd *cobra.Command) map[string]string {
	settings := map[string]string{}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if len(flag.Annotations[sdkModuleSettingAnnotation]) > 0 {
			settings[flag.Name] = flag.Value.Type()
		}
	})
	return settings
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
	for _, cmd := range moduleInitCmd.Commands() {
		moduleInitCmd.RemoveCommand(cmd)
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
