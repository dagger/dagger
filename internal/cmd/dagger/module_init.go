package daggercmd

import "github.com/dagger/dagger/core/sdk/sdkmeta"

// SDK registry policy is shared with engine migration.
func loadSDKRegistry() ([]sdkmeta.SDKEntry, error) {
	return sdkmeta.LoadRegistry()
}

func parseSDKRegistry(data []byte) ([]sdkmeta.SDKEntry, error) {
	return sdkmeta.ParseRegistry(data)
}

func sdkResolve(input string) (string, error) {
	return sdkmeta.Resolve(input)
}

func sdkResolveInstall(input string) (ref string, installName string, sdkName string, err error) {
	return sdkmeta.ResolveInstall(input)
}
