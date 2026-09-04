package daggercmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/spf13/pflag"
)

// configuredSDK is the CLI view of one SDK module installed in a workspace.
// Module commands use it to add SDK subcommands and their setting flags.
type configuredSDK struct {
	commandName string
	entry       workspace.ModuleEntry
}

func configuredSDKs(cfg *workspace.Config) ([]configuredSDK, error) {
	if cfg == nil || cfg.SDKs == nil {
		return nil, nil
	}
	if err := workspace.ValidateSDKs(cfg); err != nil {
		return nil, err
	}
	sdks := make([]configuredSDK, 0, len(cfg.SDKs))
	for commandName, sdk := range cfg.SDKs {
		sdks = append(sdks, configuredSDK{
			commandName: commandName,
			entry:       cfg.Modules[sdk.Module],
		})
	}
	sort.Slice(sdks, func(i, j int) bool { return sdks[i].commandName < sdks[j].commandName })
	return sdks, nil
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

func readSelectedWorkspaceConfig(ctx context.Context, dag *dagger.Client) (*workspace.Config, string, error) {
	ws := dag.CurrentWorkspace()
	cfgPath, err := ws.ConfigFile(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("load selected workspace config file: %w", err)
	}
	if cfgPath == "" {
		return nil, "", nil
	}

	data, err := ws.ConfigRead(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("read selected workspace config %q: %w", cfgPath, err)
	}
	cfg, err := workspace.ParseConfig([]byte(data))
	if err != nil {
		return nil, "", fmt.Errorf("parse selected workspace config %q: %w", cfgPath, err)
	}
	return cfg, cfgPath, nil
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

func sdkInitFlagValue(flag *pflag.Flag) any {
	if getter, ok := flag.Value.(interface{ Get() any }); ok {
		return getter.Get()
	}
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		return slice.GetSlice()
	}
	return flag.Value.String()
}
