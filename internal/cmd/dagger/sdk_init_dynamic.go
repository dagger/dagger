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
	commands := make(map[string]*cobra.Command, len(sdks))
	for _, sdk := range sdks {
		cmd := newInstalledSDKCommand(sdk)
		sdkCmd.AddCommand(cmd)
		commands[sdk.commandName] = cmd
	}

	selectedName, ok := sdkInvocationSDKName(args)
	if !ok {
		return nil
	}
	selected, err := resolveConfiguredSDK(cfg, selectedName)
	if err != nil {
		// Leave unknown names to Cobra, which can provide suggestions from the
		// dynamic SDK command list. Only surface config ambiguity here.
		if strings.Contains(err.Error(), "ambiguous") {
			return err
		}
		return nil
	}
	parent := commands[selected.commandName]
	if parent == nil || sdkInvocationIsInfo(args) {
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
		return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger install <module-ref>`", sdkName)
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
		return configuredSDK{}, fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger install <module-ref>`", sdkName)
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
		Short: fmt.Sprintf("Use the %s SDK (module: %s)", sdk.commandName, sdk.moduleName),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		Annotations: map[string]string{dynamicSDKCommandAnnotation: "true"},
	}
	cmd.AddCommand(newSDKInfoCommand(sdk.commandName))
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
		moduleCmd, err := newSDKModuleCommand(sdk.commandName, moduleFn)
		if err != nil {
			return err
		}
		parent.AddCommand(moduleCmd)
	}

	if clientFn := functions[sdkInitKindClient]; clientFn != nil {
		clientCmd, err := newSDKClientCommand(sdk.commandName, clientFn)
		if err != nil {
			return err
		}
		parent.AddCommand(clientCmd)
	}
	return nil
}

func newSDKModuleCommand(sdkName string, fn *modFunction) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "module",
		Short: fmt.Sprintf("Develop Dagger modules using the %s SDK", sdkName),
	}
	initCmd := newModuleInitSDKCommand(sdkName)
	if err := addSDKInitFunctionFlags(initCmd, fn, sdkInitKindModule); err != nil {
		return nil, err
	}
	cmd.AddCommand(
		initCmd,
		newSDKModuleClaimCommand(sdkName),
		newSDKModuleUnclaimCommand(sdkName),
		newSDKModuleClaimedCommand(sdkName),
	)
	return cmd, nil
}

func newSDKClientCommand(sdkName string, fn *modFunction) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "client",
		Short: fmt.Sprintf("Generate API clients using the %s SDK", sdkName),
	}
	initCmd := newAPIClientInitSDKCommand(sdkName)
	if err := addSDKInitFunctionFlags(initCmd, fn, sdkInitKindClient); err != nil {
		return nil, err
	}
	cmd.AddCommand(
		initCmd,
		newSDKClientClaimCommand(sdkName),
		newSDKClientUnclaimCommand(sdkName),
		newSDKClientClaimedCommand(sdkName),
	)
	return cmd, nil
}

func newModuleInitSDKCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "init <name>",
		Short:                 "Initialize a new module",
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModuleInitWithSDK(cmd, sdkName, args[0])
		},
	}
	cmd.Flags().StringVar(&moduleInitPath, "path", "", "Module path, relative to the current directory (\"/\" = workspace root; default: .dagger/modules/<name> beside dagger.toml)")
	cmd.Flags().BoolVar(&moduleInitNoGenerate, "no-generate", false, "Skip running the SDK's generators for the new module")
	cmd.SetGlobalNormalizationFunc(sdkInitFlagNormalizer)
	return cmd
}

func newAPIClientInitSDKCommand(sdkName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "init <path> <module>",
		Short:                 "Initialize a generated API client",
		Args:                  cobra.ExactArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIClientInitWithSDK(cmd, sdkName, args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&apiClientInitNoGenerate, "no-generate", false, "Skip running the SDK's generators for the new client")
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
	tokens := sdkInitCommandTokens(args)
	return len(tokens) > 0 && tokens[0] == "sdk"
}

func sdkInvocationSDKName(args []string) (string, bool) {
	tokens := sdkInitCommandTokens(args)
	if len(tokens) < 2 || tokens[0] != "sdk" {
		return "", false
	}
	return tokens[1], true
}

func sdkInvocationIsInfo(args []string) bool {
	tokens := sdkInitCommandTokens(args)
	return len(tokens) >= 3 && tokens[0] == "sdk" && tokens[2] == "info"
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
	functions, err := inspectSDKInitFunctions(ctx, dag, sdkRef)
	if err != nil {
		return nil, err
	}
	fn := functions[kind]
	if fn == nil {
		return nil, errSDKInitFunctionNotFound
	}
	return fn, nil
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
		return map[sdkInitKind]*modFunction{}, nil
	}
	provider := constructor.ReturnType.AsFunctionProvider()
	if provider == nil {
		return map[sdkInitKind]*modFunction{}, nil
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
			return nil, fmt.Errorf("inspect sdk %q init%s args: %w", sdkRef, strings.ToUpper(string(kind)[:1])+string(kind)[1:], err)
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
		"path": true,
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
