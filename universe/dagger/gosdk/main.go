package main

import "dagger.io/dagger"

func main() {
	ctx := dagger.DefaultContext()
	ctx.Client().Environment().
		WithCheck_(Targets.GoLint).
		Serve()
}

type Targets struct {
	// If set, the git repo to pull the dagger repo source code from
	Repo string
	// If set, the branch of the --repo setting
	Branch string
}

func (t Targets) srcDir(ctx dagger.Context) *dagger.Directory {
	srcDir := ctx.Client().Host().Directory(".")
	if t.Repo != "" {
		srcDir = ctx.Client().Git(t.Repo).Branch(t.Branch).Tree()
	}
	return srcDir
}

// Lint the Dagger Go SDK
// TODO: once namespacing is in place, can just name this "Lint"
func (t Targets) GoLint(ctx dagger.Context) (string, error) {
	c := ctx.Client().Pipeline("sdk").Pipeline("go").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51-alpine").
		WithMountedDirectory("/app", t.srcDir(ctx)).
		WithWorkdir("/app/sdk/go").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Stderr(ctx)
	if err != nil {
		return "", err
	}

	// TODO: test generated code matches

	return out, nil
}
