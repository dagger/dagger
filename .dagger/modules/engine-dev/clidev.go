package main

import "dagger/engine-dev/internal/dagger"

// A source passed to cli-dev skips its +ignore: the engine applies it only
// when it resolves the default itself. This copies cli-dev New's source
// pragma (.dagger/modules/cli-dev/main.go); keep it in sync.
var cliDevSourceIgnore = []string{
	"*",
	".*",
	"!cmd/dagger/*",
	"!internal/cmd/dagger/**",
	"!**/go.sum",
	"!**/go.mod",
	"!**/*.go",
	"!**/VERSION",
	"!vendor/**/*",
	"!**.graphql",
	"!.changes",
	"!LICENSE",
	"!install.sh",
	"!install.ps1",
	"!**/*.sql",
	"!core/prompts/*.md",
	"!core/sdk/sdkmeta/sdks.json",
}

func (dev *EngineDev) cliDevSource() *dagger.Directory {
	return dev.Source.Filter(dagger.DirectoryFilterOpts{Exclude: cliDevSourceIgnore})
}
