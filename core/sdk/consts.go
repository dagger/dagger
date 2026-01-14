package sdk

const (
	RuntimeWorkdirPath = "/scratch"
)

type sdk string

const (
	sdkGo         sdk = "go"
	sdkPython     sdk = "python"
	sdkTypescript sdk = "typescript"
	sdkPHP        sdk = "php"
	sdkElixir     sdk = "elixir"
	sdkJava       sdk = "java"
	sdkNushell    sdk = "nushell"
)

// this list is to format the invalid sdk msg
// and keeping that in sync with builtinSDK func
var validInbuiltSDKs = []sdk{
	sdkGo,
	sdkPython,
	sdkTypescript,
	sdkPHP,
	sdkElixir,
	sdkJava,
	sdkNushell,
}

// The list of functions that may be implemented by a SDK module.
var sdkFunctions = []string{
	"withConfig",
	"codegen",
	"moduleRuntime",
	"moduleTypes",
	"requiredClientGenerationFiles",
	"generateClient",
}
