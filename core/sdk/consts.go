package sdk

import "github.com/dagger/dagger/core/sdk/sdkmeta"

const (
	RuntimeWorkdirPath = "/scratch"
)

type sdk string

const (
	sdkGo         sdk = sdkmeta.Go
	sdkDang       sdk = sdkmeta.Dang
	sdkPython     sdk = sdkmeta.Python
	sdkTypescript sdk = sdkmeta.Typescript
	sdkPHP        sdk = sdkmeta.PHP
	sdkElixir     sdk = sdkmeta.Elixir
	sdkJava       sdk = sdkmeta.Java
)

// The list of functions that may be implemented by a SDK module.
var sdkFunctions = []string{
	"withConfig",
	"codegen",
	"moduleRuntime",
	"moduleTypes",
	"requiredClientGenerationFiles",
	"generateClient",
	"initModule",
	"initClient",
	"targetRuntime",
}
