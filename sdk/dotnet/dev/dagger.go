// Functions for managing Dagger.
package main

import "github.com/dagger/dagger/sdk/dotnet/dev/internal/dagger"

const daggerVersion = "0.13.3"

func installDaggerCli(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithEnvVariable("BIN_DIR", "/usr/local/bin").
		WithEnvVariable("DAGGER_VERSION", daggerVersion).
		WithExec([]string{"apk", "add", "--no-cache", "curl"}).
		WithExec([]string{"sh", "-c", "curl -L https://dl.dagger.io/dagger/install.sh | sh"})
}
