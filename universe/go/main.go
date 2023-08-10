package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

func main() {
	dagger.DefaultClient().Environment().
		WithFunction_(Build).
		WithFunction_(Test).
		WithFunction_(Generate).
		WithFunction_(Gotestsum).
		WithFunction_(GolangCILint).
		Serve()
}

type GoBuildOpts struct {
	Packages []string `doc:"Packages to build."`
	Subdir   string   `doc:"Subdirectory in which to place the built artifacts."`
	Xdefs    []string `doc:"-X definitions to pass to go build -ldflags."`
	Static   bool     `doc:"Whether to enable CGO."`
	Race     bool     `doc:"Whether to build with race detection."`

	GOOS   string `doc:"GOOS to pass to go build for cross-compilation."`
	GOARCH string `doc:"GOARCH to pass to go build. for cross-compilation"`

	BuildFlags []string `doc:"Arbitrary flags to pass along to go build."`
}

func Build(ctx dagger.Context, base *dagger.Container, src *dagger.Directory, opts GoBuildOpts) *dagger.Directory {
	ctr := base.
		With(GlobalCache(ctx)).
		WithDirectory("/out", ctx.Client().Directory()).
		With(Cd("/src", src))

	if opts.Static {
		ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")
	}

	if opts.GOOS != "" {
		ctr = ctr.WithEnvVariable("GOOS", opts.GOOS)
	}

	if opts.GOARCH != "" {
		ctr = ctr.WithEnvVariable("GOARCH", opts.GOARCH)
	}

	cmd := []string{
		"go", "build",
		"-o", "/out/",
		"-trimpath", // unconditional for reproducible builds
	}

	if opts.Race {
		cmd = append(cmd, "-race")
	}

	cmd = append(cmd, opts.BuildFlags...)

	if len(opts.Xdefs) > 0 {
		cmd = append(cmd, "-ldflags", "-X "+strings.Join(opts.Xdefs, " -X "))
	}

	cmd = append(cmd, opts.Packages...)

	out := ctr.
		WithExec(cmd).
		Directory("/out")

	if opts.Subdir != "" {
		out = ctx.Client().
			Directory().
			WithDirectory(opts.Subdir, out)
	}

	return out
}

type GoTestOpts struct {
	Packages  []string
	Race      bool
	Verbose   bool
	TestFlags []string
}

func Test(
	ctx dagger.Context,
	base *dagger.Container,
	src *dagger.Directory,
	opts GoTestOpts,
) (*dagger.EnvironmentCheckResult, error) {
	withCode := base.
		With(GlobalCache(ctx)).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src")

	pkgs := opts.Packages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	listCmd := []string{"go", "test", "-list=^Test", "-json"}
	listCmd = append(listCmd, pkgs...)

	jsonOut, err := withCode.WithExec(listCmd).Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tests: %w", err)
	}

	dec := json.NewDecoder(bytes.NewBufferString(jsonOut))

	type testOut struct {
		// Time time.Time
		Action  string
		Package string
		Output  string
	}

	// package => pkgTests
	pkgTests := map[string][]string{}

	for {
		var out testOut
		if err := dec.Decode(&out); err != nil {
			break
		}

		if out.Action != "output" {
			continue
		}

		if !strings.HasPrefix(out.Output, "Test") {
			// "ok  \t..."
			continue
		}

		pkgTests[out.Package] = append(pkgTests[out.Package], strings.Fields(out.Output)...)
	}

	goTest := []string{"go", "test"}

	if opts.Race {
		goTest = append(goTest, "-race")
	}

	if opts.Verbose {
		goTest = append(goTest, "-v")
	}

	goTest = append(goTest, opts.TestFlags...)

	checks := ctx.Client().EnvironmentCheck()

	for pkg, tests := range pkgTests {
		for _, name := range tests {
			// TODO: including pkg would be ideal, but wreaks havok once handled as a gql field name...
			// Maybe add a description field to CheckResult?
			// checkName := path.Join(pkg, name)
			checkName := name

			onlyTest := append(goTest, "-run", "^"+name+"$", pkg)

			checks = checks.WithSubcheck(
				ctx.Client().EnvironmentCheck().
					WithName(checkName).
					WithContainer(withCode.WithFocus().WithExec(onlyTest).WithoutFocus()),
			)
		}
	}

	return checks.Result(), nil
}

type GotestsumOpts struct {
	Packages       []string
	Format         string
	Race           bool
	GoTestFlags    []string
	GotestsumFlags []string
}

func Gotestsum(
	ctx dagger.Context,
	base *dagger.Container,
	src *dagger.Directory,
	opts GotestsumOpts,
) *dagger.Container {
	if opts.Format == "" {
		opts.Format = "testname"
	}
	cmd := []string{
		"gotestsum",
		"--no-color=false", // force color
		"--format=" + opts.Format,
	}
	cmd = append(cmd, opts.GotestsumFlags...)
	cmd = append(cmd, opts.GoTestFlags...)
	goTestFlags := []string{}
	if opts.Race {
		goTestFlags = append(goTestFlags, "-race")
	}
	if len(opts.Packages) > 0 {
		goTestFlags = append(goTestFlags, opts.Packages...)
	}
	if len(goTestFlags) > 0 {
		cmd = append(cmd, "--")
		cmd = append(cmd, goTestFlags...)
	}
	return base.
		With(GlobalCache(ctx)).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithFocus().
		WithExec(cmd).
		WithoutFocus()
}

func Generate(
	ctx dagger.Context,
	base *dagger.Container,
	src *dagger.Directory,
) *dagger.Directory {
	return base.
		With(GlobalCache(ctx)).
		With(Cd("/src", src)).
		WithFocus().
		WithExec([]string{"go", "generate", "./..."}).
		WithoutFocus().
		Directory("/src")
}

type GolangCILintOpts struct {
	Verbose bool
	Timeout int
}

func GolangCILint(
	ctx dagger.Context,
	base *dagger.Container,
	src *dagger.Directory,
	opts GolangCILintOpts,
) *dagger.Container {
	cmd := []string{"golangci-lint", "run"}
	if opts.Verbose {
		cmd = append(cmd, "--verbose")
	}
	if opts.Timeout > 0 {
		cmd = append(cmd, fmt.Sprintf("--timeout=%ds", opts.Timeout))
	}
	return base.
		With(GlobalCache(ctx)).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithFocus().
		WithExec(cmd).
		WithoutFocus()
}
