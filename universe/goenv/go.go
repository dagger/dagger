package goenv

import (
	"strings"

	"dagger.io/dagger"
)

// TODO: integrate this into the API, have it cd into /absddfksdf so it doesn't
// have to take an arg?
func Cd(dst string, src *dagger.Directory) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedDirectory(dst, src).
			WithWorkdir(dst)
	}
}

func GlobalCache(ctx dagger.Context) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedCache("/go/pkg/mod", ctx.Client().CacheVolume("go-mod")).
			WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
			WithMountedCache("/go/build-cache", ctx.Client().CacheVolume("go-build")).
			WithEnvVariable("GOCACHE", "/go/build-cache")
	}
}

func BinPath(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithEnvVariable("GOBIN", "/go/bin").
		WithEnvVariable("PATH", "$GOBIN:$PATH", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

type GoBuildOpts struct {
	// Packages to build.
	Packages []string

	// Optional subdirectory in which to place the built
	// artifacts.
	Subdir string

	// -X definitions to pass to go build -ldflags.
	Xdefs []string

	// Whether to enable CGO.
	Static bool

	// Whether to build with race detection.
	Race bool

	// Cross-compile via GOOS and GOARCH.
	GOOS, GOARCH string

	// Arbitrary flags to pass along to go build.
	BuildFlags []string
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
	opts_ ...GoTestOpts,
) *dagger.Container {
	var opts GoTestOpts
	if len(opts_) > 0 {
		opts = opts_[0]
	}
	cmd := []string{"go", "test"}
	if opts.Race {
		cmd = append(cmd, "-race")
	}
	if opts.Verbose {
		cmd = append(cmd, "-v")
	}
	cmd = append(cmd, opts.TestFlags...)
	if len(opts.Packages) > 0 {
		cmd = append(cmd, opts.Packages...)
	} else {
		cmd = append(cmd, "./...")
	}
	return base.
		With(GlobalCache(ctx)).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithFocus().
		WithExec(cmd).
		WithoutFocus()
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
	opts_ ...GotestsumOpts,
) *dagger.Container {
	var opts GotestsumOpts
	if len(opts_) > 0 {
		opts = opts_[0]
	}
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
