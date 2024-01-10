// Package consts exists to facilitate sharing values between our CI infra and
// dependent code (e.g. SDKs).
//
// These are kept separate from all other code to avoid breakage from
// backwards-incompatible changes (internal/mage/ uses stable SDK, core/ uses
// dev).
package distconsts

const (
	EngineShimPath = "/usr/local/bin/dagger-shim"

	EngineDefaultStateDir = "/var/lib/dagger"

	GoSDKEngineContainerTarballPath        = "/usr/local/share/dagger/go-module-sdk-image.tar"
	PythonSDKEngineContainerModulePath     = "/usr/local/share/dagger/python-sdk/runtime"
	TypescriptSDKEngineContainerModulePath = "/usr/local/share/dagger/typescript-sdk/runtime"
)
