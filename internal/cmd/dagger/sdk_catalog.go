package daggercmd

import (
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/core/workspace"
)

// workspaceSDKCatalog is the shared, config-only view used by help, dispatch,
// guidance and install-and-initialize. Installed command names always win.
type workspaceSDKCatalog struct {
	Installed []configuredSDK
	Official  []officialSDK
}

type officialSDK struct {
	sdkmeta.SDKEntry
	InstalledName string
	// InstallName names the provider module; RegisteredName names its SDK.
	InstallName    string
	RegisteredName string
}

type sdkResolution struct {
	InstalledName string
	Official      *officialSDK
}

func classifyWorkspaceSDKs(cfg *workspace.Config) (*workspaceSDKCatalog, error) {
	installed, err := configuredSDKs(cfg)
	if err != nil {
		return nil, err
	}
	registry, err := sdkmeta.LoadRegistry()
	if err != nil {
		return nil, err
	}
	catalog := &workspaceSDKCatalog{Installed: installed}
	for _, entry := range registry {
		official := officialSDK{SDKEntry: entry}
		for _, sdk := range installed {
			if workspace.IsLocalRef(sdk.entry.Source, sdk.entry.Pin) {
				continue
			}
			resolved, ok, err := sdkmeta.Lookup(sdk.entry.Source)
			if err != nil {
				return nil, err
			}
			if ok && resolved.Repo == entry.Repo {
				official.InstalledName = sdk.commandName
				break
			}
		}
		for n := 1; ; n++ {
			name := entry.Name
			if n > 1 {
				name = fmt.Sprintf("%s-%d", name, n)
			}
			installName := sdkmeta.InstallNamePrefix + path.Base(entry.Repo)
			if n > 1 {
				installName = sdkmeta.InstallNamePrefix + name + "-sdk"
			}
			name = workspace.ConventionalSDKName(installName)
			if cfg != nil {
				_, sdkTaken := cfg.SDKs[name]
				_, moduleTaken := cfg.Modules[installName]
				_, lookupTaken := cfg.SDKs[installName]
				_, commandTaken := cfg.Modules[name]
				if sdkTaken || moduleTaken || lookupTaken || commandTaken {
					continue
				}
			}
			official.InstallName, official.RegisteredName = installName, name
			break
		}
		catalog.Official = append(catalog.Official, official)
	}
	return catalog, nil
}

// Resolve applies installed-name precedence, then registry lookup. Call
// InitError before dispatching an address: module init never accepts addresses.
func (c *workspaceSDKCatalog) Resolve(name string) sdkResolution {
	for _, sdk := range c.Installed {
		if sdk.commandName == name {
			return sdkResolution{InstalledName: name}
		}
	}
	entry, ok, _ := sdkmeta.Lookup(name)
	if ok {
		for i := range c.Official {
			if c.Official[i].Repo == entry.Repo {
				return sdkResolution{InstalledName: c.Official[i].InstalledName, Official: &c.Official[i]}
			}
		}
	}
	return sdkResolution{}
}

// InstallCommand sets the module name explicitly so the install convention
// registers exactly RegisteredName, including when a workspace name is taken.
func (s officialSDK) InstallCommand() string {
	return s.installCommandFor(s.Repo)
}

func (s officialSDK) installCommandFor(address string) string {
	return "dagger module install " + shellQuote(address) + " --name " + shellQuote(s.InstallName)
}

func (s officialSDK) installSteps(address string) string {
	return fmt.Sprintf("Install the %s SDK (%s), registering SDK name %q:\n  %s\nThen initialize a module:\n  dagger module init %s",
		s.Name, s.Repo, s.RegisteredName, s.installCommandFor(address), shellQuote(s.RegisteredName))
}

// InitError returns nil only for an installed SDK's exact command name.
// The opt-in flow can use Resolve to handle known names, and reuse InitError
// unchanged for addresses and unknown names. Neither helper fetches or writes.
func (c *workspaceSDKCatalog) InitError(name string) error {
	if strings.ContainsAny(name, "/@") {
		entry, ok, err := sdkmeta.Lookup(name)
		if err != nil {
			return err
		}
		if ok {
			for _, official := range c.Official {
				if official.Repo != entry.Repo {
					continue
				}
				if official.InstalledName != "" {
					return fmt.Errorf("module init does not accept SDK addresses. The %s SDK (%s) is already installed as %q. Run:\n  dagger module init %s", official.Name, official.Repo, official.InstalledName, shellQuote(official.InstalledName))
				}
				return fmt.Errorf("module init does not accept SDK addresses. %s", official.installSteps(name))
			}
		}
		return fmt.Errorf("module init does not accept SDK addresses. Install the SDK first:\n  dagger module install %s\nFind its registered SDK name with `dagger sdk list`, then run:\n  dagger module init <registered-name>", shellQuote(name))
	}
	resolved := c.Resolve(name)
	if resolved.InstalledName != "" {
		if resolved.InstalledName == name {
			return nil
		}
		return fmt.Errorf("the %s SDK (%s) is installed as %q. Run:\n  dagger module init %s", resolved.Official.Name, resolved.Official.Repo, resolved.InstalledName, shellQuote(resolved.InstalledName))
	}
	if s := resolved.Official; s != nil {
		message := fmt.Sprintf("the %s SDK (%s) is not installed in this workspace.\n", s.Name, s.Repo)
		if s.Name == sdkmeta.Dang {
			message += "The built-in Dang runtime can run existing Dang modules, but creating a new Dang module requires installing the Dang SDK.\n"
		}
		return fmt.Errorf("%s%s\nOr install and initialize together:\n  dagger module init %s --install-sdk", message, s.installSteps(s.Repo), shellQuote(name))
	}
	installed := make([]string, 0, len(c.Installed))
	for _, sdk := range c.Installed {
		installed = append(installed, sdk.commandName)
	}
	installedNames := strings.Join(installed, ", ")
	if installedNames == "" {
		installedNames = "(none)"
	}
	known := make([]string, 0, len(c.Official))
	for _, sdk := range c.Official {
		name := sdk.Name
		if len(sdk.Aliases) > 0 {
			name += " (" + strings.Join(sdk.Aliases, ", ") + ")"
		}
		known = append(known, name)
	}
	return fmt.Errorf("SDK %q is neither installed nor a known official SDK.\nInstalled SDKs: %s\nKnown official SDKs: %s\nFind other SDKs with `dagger module search`", name, installedNames, strings.Join(known, ", "))
}

func (c *workspaceSDKCatalog) AvailableHelp() string {
	var b strings.Builder
	for _, sdk := range c.Official {
		if sdk.InstalledName != "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("OFFICIAL SDKs AVAILABLE TO INSTALL\n")
		}
		fmt.Fprintf(&b, "  %s  %s (registers %s)\n    %s\n", sdk.Name, sdk.Repo, sdk.RegisteredName, sdk.InstallCommand())
	}
	return strings.TrimSuffix(b.String(), "\n")
}
