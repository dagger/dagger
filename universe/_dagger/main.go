package main

import (
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := dagger.DefaultContext()
	ctx.Client().Environment().
		WithCommand_(Cli).
		WithCheck_(Lint).
		WithShell_(DevShell).
		// WithExtension(ctx.Client().Environment().LoadFromUniverse("dagger/gosdk"), "go").
		Serve()
}

func srcDir(ctx dagger.Context) *dagger.Directory {
	// TODO: shouldn't be needed, try deleting
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	srcDir := ctx.Client().Host().Directory(cwd)
	return srcDir
}

// Lint everything (engine, sdks, etc)
func Lint(ctx dagger.Context) (*dagger.EnvironmentCheck, error) {
	return ctx.Client().EnvironmentCheck().
		WithSubcheck_(EngineLint).
		WithSubcheck_(PythonLint).
		WithSubcheck_(NodejsLint), nil
	// TODO: WithSubcheck_(ctx.Client().Environment().LoadFromUniverse("dagger/gosdk").Check("goLint")), nil
}
