package daggercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/spf13/cobra"
)

const dynamicSDKInitCommandAnnotation = "dynamic-sdk-init"

type sdkInitCapabilities struct {
	moduleInit bool
	clientInit bool
}

func registerInstalledSDKInitCommands() error {
	cfg, err := readWorkspaceConfigForSDKInitRegistration()
	if err != nil {
		return err
	}
	registerSDKInitCommandsFromConfig(moduleInitCmd, apiClientInitCmd, cfg)
	return nil
}

func registerSDKInitCommandsFromConfig(moduleParent, clientParent *cobra.Command, cfg *workspace.Config) {
	clearDynamicSDKInitCommands(moduleParent)
	clearDynamicSDKInitCommands(clientParent)
	if cfg == nil {
		return
	}

	sdkNames := make([]string, 0, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		if entry.AsSDK != nil {
			sdkNames = append(sdkNames, name)
		}
	}
	sort.Strings(sdkNames)

	for _, sdkName := range sdkNames {
		caps := initCapabilitiesForInstalledSDK(cfg.Modules[sdkName])
		if caps.moduleInit {
			moduleParent.AddCommand(newModuleInitSDKCommand(sdkName))
		}
		if caps.clientInit {
			clientParent.AddCommand(newAPIClientInitSDKCommand(sdkName))
		}
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

func initCapabilitiesForInstalledSDK(_ workspace.ModuleEntry) sdkInitCapabilities {
	// The SDK contract will replace this default with real initModule/initClient
	// capability detection. Until then, an installed SDK routes to the existing
	// generic Workspace.moduleInit/clientInit planning paths.
	return sdkInitCapabilities{moduleInit: true, clientInit: true}
}

func newModuleInitSDKCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
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
}

func newAPIClientInitSDKCommand(sdkName string) *cobra.Command {
	return &cobra.Command{
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
}

func readWorkspaceConfigForSDKInitRegistration() (*workspace.Config, error) {
	root, ok, err := sdkInitConfigSearchRoot()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	cfg, _, err := readWorkspaceConfigForSDKInitRegistrationFrom(root)
	return cfg, err
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
	tokens := sdkInitCommandTokens(args)
	for i := 0; i < len(tokens); i++ {
		if i+1 < len(tokens) && tokens[i] == "module" && tokens[i+1] == "init" {
			return true
		}
		if i+2 < len(tokens) && tokens[i] == "api" && tokens[i+1] == "client" && tokens[i+2] == "init" {
			return true
		}
	}
	return false
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
