package sdk

import "errors"

// WorkspaceModule describes the SDK module a workspace should install for a
// child module runtime.
type WorkspaceModule struct {
	Name   string
	Source string
}

// WorkspaceModuleForRuntime returns the workspace module that exposes a
// built-in runtime SDK. Unknown external SDK refs are intentionally left for
// the normal SDK loader and do not have a static workspace module mapping here.
func WorkspaceModuleForRuntime(runtime string) (WorkspaceModule, bool, error) {
	sdkName, suffix, err := parseSDKName(runtime)
	if errors.Is(err, errUnknownBuiltinSDK) {
		return WorkspaceModule{}, false, nil
	}
	if err != nil {
		return WorkspaceModule{}, false, err
	}

	mod, ok := workspaceModuleForBuiltinSDK(sdkName, suffix)
	return mod, ok, nil
}

func workspaceModuleForBuiltinSDK(sdkName sdk, suffix string) (WorkspaceModule, bool) {
	switch sdkName {
	case sdkGo:
		return WorkspaceModule{Name: "go-sdk", Source: "github.com/dagger/go-sdk"}, true
	case sdkDang:
		return WorkspaceModule{Name: "dang-sdk", Source: "github.com/dagger/dang-sdk"}, true
	case sdkPython:
		return WorkspaceModule{Name: "python-sdk", Source: "github.com/dagger/python-sdk"}, true
	case sdkTypescript:
		return WorkspaceModule{Name: "typescript-sdk", Source: "github.com/dagger/typescript-sdk"}, true
	case sdkJava:
		return WorkspaceModule{Name: "java-sdk", Source: "github.com/dagger/dagger/sdk/java" + suffix}, true
	case sdkPHP:
		return WorkspaceModule{Name: "php-sdk", Source: "github.com/dagger/dagger/sdk/php" + suffix}, true
	case sdkElixir:
		return WorkspaceModule{Name: "elixir-sdk", Source: "github.com/dagger/dagger/sdk/elixir" + suffix}, true
	default:
		return WorkspaceModule{}, false
	}
}
