package main

import (
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := dagger.DefaultContext()
	ctx.Client().Environment().
		WithCommand_(Cli).
		WithCheck_(EngineLint).
		WithCheck_(NodejsLint).
		WithShell_(DevShell).
		// WithExtension(ctx.Client().Environment().LoadFromUniverse("dagger/gosdk"), "go").
		Serve()
}

// TODO: re-add Lint everything target

func srcDir(ctx dagger.Context) *dagger.Directory {
	// TODO: shouldn't be needed, try deleting
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	srcDir := ctx.Client().Host().Directory(cwd)
	return srcDir
}
