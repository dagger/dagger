package workspace

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ModuleInitPlan records installation decisions separately from explicit controls.
type ModuleInitPlan struct {
	Install             bool
	Entrypoint          bool
	AutomaticInstall    bool
	AutomaticEntrypoint bool
}

// PlanModuleInit preserves the implicit rules unless a control is supplied.
func PlanModuleInit(explicitPath, explicitName bool, install, entrypoint *bool) (ModuleInitPlan, error) {
	if install != nil && !*install && entrypoint != nil && *entrypoint {
		return ModuleInitPlan{}, fmt.Errorf("--install=false cannot be combined with --entrypoint")
	}
	plan := ModuleInitPlan{Install: !explicitPath}
	if install != nil {
		plan.Install = *install
	}
	plan.Entrypoint = plan.Install && !explicitPath && !explicitName
	if entrypoint != nil {
		plan.Entrypoint = *entrypoint
	}
	if plan.Entrypoint {
		plan.Install = true
	}
	plan.AutomaticInstall = plan.Install && install == nil && (entrypoint == nil || !*entrypoint)
	plan.AutomaticEntrypoint = plan.Entrypoint && entrypoint == nil
	return plan, nil
}

// ModuleInitName infers a module name before the SDK chooses its default path.
func ModuleInitName(name, scopePath, configDir, rootName string) (string, error) {
	if name == "" && scopePath != "" {
		name = moduleDirectoryName(scopePath)
	}
	if name == "" {
		name = moduleDevName(moduleDirectoryName(configDir))
	}
	if name == "" {
		name = moduleDevName(rootName)
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("cannot infer module name from the scope path, active config file, or workspace root; pass --name to module init or set the scope name")
	}
	return name, nil
}

func moduleDevName(directoryName string) string {
	if directoryName == "" {
		return ""
	}
	return directoryName + "-dev"
}

func moduleDirectoryName(workspacePath string) string {
	cleaned := path.Clean(strings.ReplaceAll(filepath.ToSlash(workspacePath), `\`, "/"))
	if cleaned == "." || cleaned == "/" || cleaned == "" {
		return ""
	}
	return path.Base(cleaned)
}

// ModuleRootDirectoryName uses a local root or its public workspace address.
func ModuleRootDirectoryName(hostPath, address, cwd string) string {
	if hostPath := strings.TrimSpace(hostPath); hostPath != "" {
		return moduleDirectoryName(hostPath)
	}

	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	if strings.Contains(address, "://") {
		parsed, err := url.Parse(address)
		if err != nil {
			return ""
		}
		switch parsed.Scheme {
		case "file":
			address = parsed.Path
		case "http", "https", "ssh":
			address = parsed.Host + parsed.Path
		default:
			return ""
		}
	}

	address = filepath.ToSlash(address)
	cwd = filepath.Clean(cwd)
	if cwd != "." {
		suffix := "/" + filepath.ToSlash(cwd)
		if versioned := strings.LastIndex(address, suffix+"@"); versioned >= 0 {
			address = address[:versioned]
		} else if strings.HasSuffix(address, suffix) {
			address = strings.TrimSuffix(address, suffix)
		} else {
			return ""
		}
	} else if version := strings.LastIndex(address, "@"); version > strings.Index(address, "/") {
		address = address[:version]
	}
	return moduleDirectoryName(address)
}
