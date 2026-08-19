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

const dynamicSDKCommandAnnotation = "dynamic-sdk"
const sdkInitArgAnnotation = "sdk-init-arg"

type sdkInitKind string

const (
	sdkInitKindModule sdkInitKind = "module"
	sdkInitKindClient sdkInitKind = "client"
)

func registerInstalledSDKCommands(ctx context.Context, args []string) error {
	cfg, cfgPath, err := readWorkspaceConfigForSDKInitRegistration()
	if err != nil {
		return err
	}
	clearDynamicSDKCommands(sdkCmd)
	if cfg == nil {
		return nil
	}

	sdks, err := configuredSDKs(cfg)
	if err != nil {
		return err
	}
	selectedName, selectedInvocation := sdkInvocationSDKName(args)
	selected, selectedOK := lookupConfiguredSDK(cfg, selectedName)
	var parent *cobra.Command
	for _, sdk := range sdks {
		cmd := newInstalledSDKCommand(sdk)
		sdkCmd.AddCommand(cmd)
		if selectedOK && sdk.moduleName == selected.moduleName {
			parent = cmd
		}
	}

	if !selectedInvocation || !selectedOK {
		return nil
	}
	if !sdkInvocationNeedsInit(args) {
		return nil
	}

	// The SDK's init signatures define both capability presence and any custom
	// flags on the init commands, so build the selected subtree from engine
	// introspection before Cobra parses it. A discard frontend keeps this
	// preflight session separate from the command's real progress frontend.
	cfgDir := filepath.Dir(cfgPath)
	return withEngineSilent(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		return registerSDKCapabilityCommands(ctx, ec.Dagger(), parent, selected, cfgDir)
	})
}

func lookupConfiguredSDK(cfg *workspace.Config, name string) (configuredSDK, bool) {
	sdk, err := resolveConfiguredSDK(cfg, name)
	return sdk, err == nil
}

type configuredSDK struct {
	moduleName  string
	commandName string
	entry       workspace.ModuleEntry
}

func sdkCommandName(moduleName string, entry workspace.ModuleEntry) string {
	return workspace.EffectiveSDKName(moduleName, entry)
}

func configuredSDKs(cfg *workspace.Config) ([]configuredSDK, error) {
	if cfg == nil || cfg.Modules == nil {
		return nil, nil
	}
	if err := workspace.ValidateSDKNames(cfg); err != nil {
		return nil, err
	}
	sdks := make([]configuredSDK, 0, len(cfg.Modules))
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		commandName := sdkCommandName(moduleName, entry)
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
		return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger install <module-ref>`", sdkName)
	}
	if err := workspace.ValidateSDKNames(cfg); err != nil {
		return configuredSDK{}, err
	}
	if entry, ok := cfg.Modules[sdkName]; ok && entry.AsSDK != nil {
		return configuredSDK{
			moduleName:  sdkName,
			commandName: sdkCommandName(sdkName, entry),
			entry:       entry,
		}, nil
	}

	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK != nil && sdkCommandName(moduleName, entry) == sdkName {
			return configuredSDK{
				moduleName:  moduleName,
				commandName: sdkCommandName(moduleName, entry),
				entry:       entry,
			}, nil
		}
	}
	return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger install <module-ref>`", sdkName)
}

func clearDynamicSDKCommands(parent *cobra.Command) {
	for {
		removed := false
		for _, cmd := range parent.Commands() {
			if cmd.Annotations[dynamicSDKCommandAnnotation] == "true" {
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

func newInstalledSDKCommand(sdk configuredSDK) *cobra.Command {
	cmd := &cobra.Command{
		Use:   sdk.commandName,
		Short: fmt.Sprintf("Use the %s SDK to develop and consume modules", sdk.commandName),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		Annotations: map[string]string{dynamicSDKCommandAnnotation: "true"},
	}
	cmd.AddCommand(
		newSDKInfoCommand(sdk.commandName),
		newSDKModuleCommand(sdk.commandName),
		newSDKClientCommand(sdk.commandName),
	)
	return cmd
}

func registerSDKCapabilityCommands(
	ctx context.Context,
	dag *dagger.Client,
	parent *cobra.Command,
	sdk configuredSDK,
	cfgDir string,
) error {
	sdkRef, err := sdkInitModuleEntrySource(sdk.entry, cfgDir)
	if err != nil {
		return err
	}

	functions, err := inspectSDKInitFunctions(ctx, dag, sdkRef)
	if err != nil {
		return err
	}
	if moduleFn := functions[sdkInitKindModule]; moduleFn != nil {
		moduleCmd, _, err := parent.Find([]string{"module"})
		if err != nil {
			return err
		}
		if err := addSDKModuleInitCommand(moduleCmd, sdk.commandName, moduleFn); err != nil {
			return err
		}
	}

	if clientFn := functions[sdkInitKindClient]; clientFn != nil {
		clientCmd, _, err := parent.Find([]string{"client"})
		if err != nil {
			return err
		}
		if err := addSDKClientInitCommand(clientCmd, sdk.commandName, clientFn); err != nil {
			return err
		}
	}
	return nil
}

func newSDKModuleCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: fmt.Sprintf("Develop Dagger modules using the %s SDK", sdkName),
	}
	cmd.AddCommand(
		newSDKModuleClaimCommand(sdkName),
		newSDKModuleUnclaimCommand(sdkName),
		newSDKModuleListCommand(sdkName),
	)
	return cmd
}

func addSDKModuleInitCommand(parent *cobra.Command, sdkName string, fn *modFunction) error {
	initCmd := newModuleInitSDKCommand(sdkName)
	if err := addSDKInitFunctionFlags(initCmd, fn, sdkInitKindModule); err != nil {
		return err
	}
	parent.AddCommand(initCmd)
	return nil
}

func newSDKClientCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: fmt.Sprintf("Generate API clients using the %s SDK", sdkName),
	}
	cmd.AddCommand(
		newSDKClientClaimCommand(sdkName),
		newSDKClientUnclaimCommand(sdkName),
		newSDKClientListCommand(sdkName),
	)
	return cmd
}

func addSDKClientInitCommand(parent *cobra.Command, sdkName string, fn *modFunction) error {
	initCmd := newAPIClientInitSDKCommand(sdkName)
	if err := addSDKInitFunctionFlags(initCmd, fn, sdkInitKindClient); err != nil {
		return err
	}
	parent.AddCommand(initCmd)
	return nil
}

func newModuleInitSDKCommand(sdkName string) *cobra.Command {
	var path string
	var noGenerate bool
	cmd := &cobra.Command{
		Use:                   "init <name>",
		Short:                 "Initialize a new module",
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModuleInitWithSDK(cmd, sdkName, args[0], path, noGenerate)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Module path, relative to the current directory (\"/\" = workspace root; default: .dagger/modules/<name> beside dagger.toml)")
	cmd.Flags().BoolVar(&noGenerate, "no-generate", false, "Skip running the SDK's generators for the new module")
	cmd.SetGlobalNormalizationFunc(sdkInitFlagNormalizer)
	return cmd
}

func newAPIClientInitSDKCommand(sdkName string) *cobra.Command {
	var noGenerate bool
	cmd := &cobra.Command{
		Use:                   "init <path> <module>",
		Short:                 "Initialize a generated API client",
		Args:                  cobra.ExactArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIClientInitWithSDK(cmd, sdkName, args[0], args[1], noGenerate)
		},
	}
	cmd.Flags().BoolVar(&noGenerate, "no-generate", false, "Skip running the SDK's generators for the new client")
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

func shouldRegisterSDKCommands(args []string) bool {
	return len(args) > 0 && args[0] == "sdk"
}

func sdkInvocationSDKName(args []string) (string, bool) {
	if len(args) < 2 || args[0] != "sdk" {
		return "", false
	}
	return args[1], true
}

func sdkInvocationNeedsInit(args []string) bool {
	if len(args) < 3 {
		return true
	}
	if args[2] == "info" {
		return false
	}
	if args[2] == "help" {
		args = append([]string{args[0], args[1]}, args[3:]...)
		if len(args) < 3 {
			return true
		}
	}
	if args[2] != "module" && args[2] != "client" {
		return false
	}
	if len(args) < 4 {
		return true
	}
	switch args[3] {
	case "claim", "list", "unclaim":
		return false
	default:
		return true
	}
}

func sdkInitModuleEntrySource(entry workspace.ModuleEntry, cfgDir string) (string, error) {
	source := sdkModuleEntrySource(entry)
	if source == "" {
		return "", fmt.Errorf("SDK module entry has no source")
	}
	if workspace.IsLocalRef(entry.Source, entry.Pin) {
		source = filepath.Join(cfgDir, source)
	}
	return source, nil
}

func sdkModuleEntrySource(entry workspace.ModuleEntry) string {
	source := entry.Source
	if entry.Pin != "" && !strings.Contains(source, "@") {
		source += "@" + entry.Pin
	}
	return source
}

func inspectSDKInitFunctions(
	ctx context.Context,
	dag *dagger.Client,
	sdkRef string,
) (map[sdkInitKind]*modFunction, error) {
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
		return nil, nil
	}
	provider := constructor.ReturnType.AsFunctionProvider()
	if provider == nil {
		return nil, nil
	}

	functions := map[sdkInitKind]*modFunction{}
	for _, candidate := range provider.GetFunctions() {
		switch {
		case candidate.Name == "initModule" || candidate.CmdName() == "initModule":
			functions[sdkInitKindModule] = candidate
		case candidate.Name == "initClient" || candidate.CmdName() == "initClient":
			functions[sdkInitKindClient] = candidate
		}
	}
	for kind, fn := range functions {
		if err := mod.LoadFunctionTypeDefs(fn); err != nil {
			return nil, fmt.Errorf("inspect sdk %q %s init args: %w", sdkRef, kind, err)
		}
	}
	return functions, nil
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
		"path":       true,
		"noGenerate": true,
	}
	if kind == sdkInitKindModule {
		standard["name"] = true
	} else {
		standard["module"] = true
	}

	extra := make([]*modFunctionArg, 0, len(fn.Args))
	for _, arg := range fn.Args {
		if standard[arg.Name] || arg.IsWorkspace() {
			continue
		}
		extra = append(extra, arg)
	}
	return extra
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
