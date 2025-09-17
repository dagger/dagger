package sdk

import "slices"

// Return true if the given module is a builtin SDK.
func IsModuleSDKBuiltin(module string) bool {
	return slices.Contains(validInbuiltSDKs, sdk(module))
}
