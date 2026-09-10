package daggercmd

import (
	"sort"
	"strings"

	"github.com/dagger/dagger/core/workspace"
)

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
