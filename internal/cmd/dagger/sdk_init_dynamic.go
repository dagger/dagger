package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const dynamicSDKInitCommandAnnotation = "dynamic-sdk-init"
const sdkInitArgAnnotation = "sdk-init-arg"

type sdkInitKind string

const (
	sdkInitKindModule sdkInitKind = "module"
	sdkInitKindClient sdkInitKind = "client"
)

func registerInstalledSDKInitCommands(ctx context.Context, args []string) error {
	kind, ok := sdkInitInvocationKind(args)
	if !ok {
		return nil
	}

	cfg, cfgPath, err := readWorkspaceConfigForSDKInitRegistration()
	if err != nil {
		return err
	}
	if cfg == nil {
		clearDynamicSDKInitCommands(moduleInitCmd)
		clearDynamicSDKInitCommands(apiClientInitCmd)
		return nil
	}

	cfgDir := filepath.Dir(cfgPath)
	// Build the dynamic subcommands under a throwaway discard frontend rather
	// than the main one. This SDK introspection runs before the real command
	// executes; the pretty TUI frontend is single-shot (it spawns its run
	// function only once, guarded by fe.spawned, which is never reset), so
	// driving Frontend.Run here would consume that one run and leave the
	// actual command's Frontend.Run hanging without ever spawning its work.
	return withEngineSilent(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return registerSDKInitCommandsFromConfigForKind(ctx, ec.Dagger(), moduleInitCmd, apiClientInitCmd, cfg, cfgDir, kind)
	})
}

func registerSDKInitCommandsFromConfigForKind(
	ctx context.Context,
	dag *dagger.Client,
	moduleParent, clientParent *cobra.Command,
	cfg *workspace.Config,
	cfgDir string,
	kind sdkInitKind,
) error {
	clearDynamicSDKInitCommands(moduleParent)
	clearDynamicSDKInitCommands(clientParent)
	if cfg == nil {
		return nil
	}

	sdks, err := configuredSDKs(cfg)
	if err != nil {
		return err
	}

	for _, sdk := range sdks {
		sdkRef, err := sdkInitModuleEntrySource(sdk.entry, cfgDir)
		if err != nil {
			return err
		}
		fn, err := inspectSDKInitFunction(ctx, dag, sdkRef, kind)
		if errors.Is(err, errSDKInitFunctionNotFound) {
			continue
		}
		if err != nil {
			return err
		}

		switch kind {
		case sdkInitKindModule:
			cmd := newModuleInitSDKCommand(sdk.commandName)
			if err := addSDKInitFunctionFlags(cmd, fn, kind); err != nil {
				return err
			}
			moduleParent.AddCommand(cmd)
		case sdkInitKindClient:
			cmd := newAPIClientInitSDKCommand(sdk.commandName)
			if err := addSDKInitFunctionFlags(cmd, fn, kind); err != nil {
				return err
			}
			clientParent.AddCommand(cmd)
		}
	}
	return nil
}

type configuredSDK struct {
	moduleName  string
	commandName string
	entry       workspace.ModuleEntry
}

func sdkCommandName(moduleName string, entry workspace.ModuleEntry) string {
	if entry.AsSDK != nil && entry.AsSDK.Name != "" {
		return entry.AsSDK.Name
	}
	return moduleName
}

func configuredSDKs(cfg *workspace.Config) ([]configuredSDK, error) {
	if cfg == nil || cfg.Modules == nil {
		return nil, nil
	}
	sdks := make([]configuredSDK, 0, len(cfg.Modules))
	seen := map[string]string{}
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		commandName := sdkCommandName(moduleName, entry)
		if existing, ok := seen[commandName]; ok {
			return nil, fmt.Errorf("SDK command name %q is ambiguous: modules.%s.as-sdk and modules.%s.as-sdk both use it", commandName, existing, moduleName)
		}
		seen[commandName] = moduleName
		sdks = append(sdks, configuredSDK{
			moduleName:  moduleName,
			commandName: commandName,
			entry:       entry,
		})
	}
	sort.Slice(sdks, func(i, j int) bool {
		if sdks[i].commandName != sdks[j].commandName {
			return sdks[i].commandName < sdks[j].commandName
		}
		return sdks[i].moduleName < sdks[j].moduleName
	})
	return sdks, nil
}

func resolveConfiguredSDK(cfg *workspace.Config, sdkName string) (configuredSDK, error) {
	if cfg == nil || cfg.Modules == nil {
		return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", sdkName, sdkName)
	}
	if entry, ok := cfg.Modules[sdkName]; ok && entry.AsSDK != nil {
		return configuredSDK{
			moduleName:  sdkName,
			commandName: sdkCommandName(sdkName, entry),
			entry:       entry,
		}, nil
	}

	var matches []configuredSDK
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || entry.AsSDK.Name != sdkName {
			continue
		}
		matches = append(matches, configuredSDK{
			moduleName:  moduleName,
			commandName: sdkCommandName(moduleName, entry),
			entry:       entry,
		})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].moduleName < matches[j].moduleName })
	switch len(matches) {
	case 0:
		return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", sdkName, sdkName)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, match := range matches {
			names[i] = match.moduleName
		}
		return configuredSDK{}, fmt.Errorf("SDK name %q is ambiguous: matches modules.%s.as-sdk; choose a unique as-sdk.name", sdkName, strings.Join(names, ".as-sdk, modules."))
	}
}

func clearDynamicSDKInitCommands(parent *cobra.Command) {
	for {
		removed := false
		for _, cmd := range parent.Commands() {
			if cmd.Annotations[dynamicSDKInitCommandAnnotation] == "true" {
				parent.RemoveCommand(cmd)
				removed = true
				break
			}
		}
		if !removed {
			return
		}
	}
}

func newModuleInitSDKCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   sdkName + " <name>",
		Short:                 fmt.Sprintf("Initialize a new module with %s", sdkName),
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModuleInitWithSDK(cmd, sdkName, args[0])
		},
		Annotations: map[string]string{
			dynamicSDKInitCommandAnnotation: "true",
		},
	}
	cmd.SetGlobalNormalizationFunc(sdkInitFlagNormalizer)
	return cmd
}

func newAPIClientInitSDKCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   sdkName + " <path> <module>",
		Short:                 fmt.Sprintf("Initialize a generated API client with %s", sdkName),
		Args:                  cobra.ExactArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIClientInitWithSDK(cmd, sdkName, args[0], args[1])
		},
		Annotations: map[string]string{
			dynamicSDKInitCommandAnnotation: "true",
		},
	}
	cmd.SetGlobalNormalizationFunc(sdkInitFlagNormalizer)
	return cmd
}

func sdkInitFlagNormalizer(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	return pflag.NormalizedName(cliName(name))
}

func readWorkspaceConfigForSDKInitRegistration() (*workspace.Config, string, error) {
	root, ok, err := sdkInitConfigSearchRoot()
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", nil
	}

	return readWorkspaceConfigForSDKInitRegistrationFrom(root)
}

func sdkInitConfigSearchRoot() (string, bool, error) {
	if workspaceRef != "" {
		if isObviouslyRemoteWorkspaceRef(workspaceRef) {
			return "", false, nil
		}
		abs, err := pathutil.Abs(workspaceRef)
		if err != nil {
			return "", false, fmt.Errorf("resolve workspace %q: %w", workspaceRef, err)
		}
		return abs, true, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("getwd: %w", err)
	}
	return cwd, true, nil
}

func readWorkspaceConfigForSDKInitRegistrationFrom(start string) (*workspace.Config, string, error) {
	dir := start
	for {
		cfgPath := filepath.Join(dir, workspace.ConfigFileName)
		if _, err := os.Stat(cfgPath); err == nil {
			data, err := os.ReadFile(cfgPath)
			if err != nil {
				return nil, "", fmt.Errorf("read workspace config %q: %w", cfgPath, err)
			}
			cfg, err := workspace.ParseConfig(data)
			if err != nil {
				return nil, "", fmt.Errorf("parse workspace config %q: %w", cfgPath, err)
			}
			return cfg, cfgPath, nil
		} else if !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("stat workspace config %q: %w", cfgPath, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", nil
		}
		dir = parent
	}
}

func shouldRegisterSDKInitCommands(args []string) bool {
	_, ok := sdkInitInvocationKind(args)
	return ok
}

func sdkInitInvocationKind(args []string) (sdkInitKind, bool) {
	tokens := sdkInitCommandTokens(args)
	for i := 0; i < len(tokens); i++ {
		if i+1 < len(tokens) && tokens[i] == "module" && tokens[i+1] == "init" {
			return sdkInitKindModule, true
		}
		if i+2 < len(tokens) && tokens[i] == "api" && tokens[i+1] == "client" && tokens[i+2] == "init" {
			return sdkInitKindClient, true
		}
	}
	return "", false
}

func sdkInitCommandTokens(args []string) []string {
	tokens := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, _, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if !hasValue && sdkInitGlobalFlagTakesValue(name) && i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if arg == "-W" && i+1 < len(args) {
				i++
			}
			continue
		}
		tokens = append(tokens, arg)
	}
	return tokens
}

func sdkInitGlobalFlagTakesValue(name string) bool {
	switch name {
	case "workdir",
		"workspace",
		"env",
		"progress",
		"lock",
		"interactive-command",
		"x-release",
		"dot-output",
		"dot-focus-field":
		return true
	default:
		return false
	}
}

var errSDKInitFunctionNotFound = errors.New("sdk init function not found")

func sdkInitModuleEntrySource(entry workspace.ModuleEntry, cfgDir string) (string, error) {
	source := entry.Source
	if source == "" {
		return "", fmt.Errorf("SDK module entry has no source")
	}
	if workspace.IsLocalRef(source, entry.Pin) {
		source = filepath.Join(cfgDir, source)
	}
	if entry.Pin != "" && !strings.Contains(source, "@") {
		source += "@" + entry.Pin
	}
	return source, nil
}

func inspectSDKInitFunction(
	ctx context.Context,
	dag *dagger.Client,
	sdkRef string,
	kind sdkInitKind,
) (*modFunction, error) {
	fnName := "initModule"
	if kind == sdkInitKindClient {
		fnName = "initClient"
	}

	modSrc := dag.ModuleSource(sdkRef)
	// Inspect the SDK's own init contract only. Serving its dependencies would
	// pull them into the session's shared module namespace, so two installed
	// SDKs that share a transitive dependency at different sources/pins (e.g.
	// each SDK pinning sdk-sdk/polyfill to a different commit) would collide
	// during registration.
	mod, err := initializeModule(ctx, dag, sdkRef, modSrc, initModuleOpts{skipDependencies: true})
	if err != nil {
		return nil, fmt.Errorf("inspect sdk %q: %w", sdkRef, err)
	}
	constructor := mod.ModuleConstructor()
	if constructor == nil || constructor.ReturnType == nil {
		return nil, errSDKInitFunctionNotFound
	}
	provider := constructor.ReturnType.AsFunctionProvider()
	if provider == nil {
		return nil, errSDKInitFunctionNotFound
	}

	var fn *modFunction
	for _, candidate := range provider.GetFunctions() {
		if candidate.Name == fnName || candidate.CmdName() == fnName {
			fn = candidate
			break
		}
	}
	if fn == nil {
		return nil, errSDKInitFunctionNotFound
	}
	if err := mod.LoadFunctionTypeDefs(fn); err != nil {
		return nil, fmt.Errorf("inspect sdk %q %s args: %w", sdkRef, fnName, err)
	}
	return fn, nil
}

func addSDKInitFunctionFlags(cmd *cobra.Command, fn *modFunction, kind sdkInitKind) error {
	args, err := sdkInitFunctionFlagArgs(fn, kind)
	if err != nil {
		return err
	}
	for _, arg := range args {
		if err := arg.AddFlag(cmd.Flags()); err != nil {
			return err
		}
		if arg.IsRequired() {
			if err := cmd.MarkFlagRequired(arg.FlagName()); err != nil {
				return err
			}
		}
		if err := cmd.Flags().SetAnnotation(arg.FlagName(), sdkInitArgAnnotation, []string{arg.Name}); err != nil {
			return err
		}
		if err := cmd.Flags().SetAnnotation(arg.FlagName(), "help:group", []string{"Arguments"}); err != nil {
			return err
		}
	}
	return nil
}

func sdkInitFunctionFlagArgs(fn *modFunction, kind sdkInitKind) ([]*modFunctionArg, error) {
	flags := pflag.NewFlagSet("sdk-init", pflag.ContinueOnError)
	args := make([]*modFunctionArg, 0, len(fn.Args))
	for _, arg := range sdkInitFunctionExtraArgs(fn, kind) {
		if err := arg.AddFlag(flags); err != nil {
			var unsupported *UnsupportedFlagError
			if errors.As(err, &unsupported) && !arg.IsRequired() {
				continue
			}
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func sdkInitFunctionExtraArgs(fn *modFunction, kind sdkInitKind) []*modFunctionArg {
	standard := map[string]bool{
		"path": true,
	}
	if kind == sdkInitKindModule {
		standard["name"] = true
	} else {
		standard["module"] = true
	}

	extra := make([]*modFunctionArg, 0, len(fn.Args))
	for _, arg := range fn.Args {
		if standard[arg.Name] || sdkInitArgIsWorkspace(arg) {
			continue
		}
		extra = append(extra, arg)
	}
	return extra
}

func sdkInitArgIsWorkspace(arg *modFunctionArg) bool {
	typeDef := arg.TypeDef
	if typeDef == nil || typeDef.Kind != dagger.TypeDefKindObjectKind || typeDef.AsObject == nil {
		return false
	}
	return typeDef.AsObject.Name == "Workspace" && typeDef.AsObject.SourceModuleName == ""
}

func sdkInitArgsJSON(cmd *cobra.Command) (string, error) {
	args := map[string]any{}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		annotations := flag.Annotations[sdkInitArgAnnotation]
		if len(annotations) == 0 {
			return
		}
		args[annotations[0]] = sdkInitFlagValue(flag)
	})
	if len(args) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode sdk init args: %w", err)
	}
	return string(encoded), nil
}

func sdkInitFlagValue(flag *pflag.Flag) any {
	if getter, ok := flag.Value.(interface{ Get() any }); ok {
		return getter.Get()
	}
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		return slice.GetSlice()
	}
	return flag.Value.String()
}
