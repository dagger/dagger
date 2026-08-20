package daggercmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
)

// sdkCmd is populated from the SDK modules installed in the current workspace
// before Cobra parses an SDK invocation. A bare `dagger sdk` therefore doubles
// as the installed-SDK list: its generated subcommands are the available SDKs.
var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Use Dagger SDKs to develop and consume modules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

// migratedSDKFixup describes a workspace SDK install that migration recorded by
// bare SDK short name and that must be resolved to its real ref and canonical
// name through the sdks.json registry.
type migratedSDKFixup struct {
	ModuleName     string
	CurrentSDKName string
	Ref            string
	SDKName        string
}

// planMigratedSDKFixups finds SDK installs whose source is a bare legacy SDK
// short name and resolves each through sdks.json. Entries already using a
// module ref/path, or a bare name absent from the registry, are left untouched.
func planMigratedSDKFixups(cfg *workspace.Config) []migratedSDKFixup {
	if cfg == nil {
		return nil
	}
	var fixups []migratedSDKFixup
	for currentSDKName, sdk := range cfg.SDKs {
		entry, ok := cfg.Modules[sdk.Module]
		if !ok || entry.Source == "" || strings.Contains(entry.Source, "/") {
			continue
		}
		base, version, _ := strings.Cut(entry.Source, "@")
		ref, _, sdkName, err := sdkResolveInstall(base)
		if err != nil {
			continue
		}
		if version != "" {
			ref += "@" + version
		}
		fixups = append(fixups, migratedSDKFixup{
			ModuleName:     sdk.Module,
			CurrentSDKName: currentSDKName,
			Ref:            ref,
			SDKName:        sdkName,
		})
	}
	sort.Slice(fixups, func(i, j int) bool { return fixups[i].ModuleName < fixups[j].ModuleName })
	return fixups
}

func readLocalWorkspaceConfig() (*workspace.Config, string, error) {
	root, local, err := sdkInitConfigSearchRoot()
	if err != nil {
		return nil, "", err
	}
	if !local {
		return nil, "", fmt.Errorf("SDK information requires a local workspace")
	}
	cfg, cfgPath, err := readWorkspaceConfigForSDKInitRegistrationFrom(root)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		return nil, "", fmt.Errorf("no workspace config (%s) found from %q upward; install a module with `dagger install <module-ref>` to create one", workspace.ConfigFileName, root)
	}
	return cfg, cfgPath, nil
}
