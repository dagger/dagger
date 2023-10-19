// Package consts exists to facilitate sharing values between our CI infra and
// dependent code (e.g. SDKs).
//
// These are kept separate from all other code to avoid breakage from
// backwards-incompatible changes (internal/mage/ uses stable SDK, core/ uses
// dev).
package consts

const (
	GoSDKEngineContainerTarballPath    = "/usr/local/share/dagger/go-module-sdk-image.tar"
	PythonSDKEngineContainerModulePath = "/usr/local/share/dagger/python-sdk/runtime"
)
