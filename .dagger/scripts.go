package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/.dagger/internal/dagger"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

type Scripts struct {
	Dagger *DaggerDev // +private
}

// Lint scripts files
func (s Scripts) Lint(ctx context.Context) error {
	eg := errgroup.Group{}
	eg.Go(func() error {
		return dag.Shellcheck().
			Check(s.Dagger.Source.File("install.sh")).
			Assert(ctx)
	})
	eg.Go(func() error {
		return dag.PsAnalyzer().
			Check(s.Dagger.Source.File("install.ps1"), dagger.PsAnalyzerCheckOpts{
				// Exclude the unused parameters for now due because PSScriptAnalyzer treat
				// parameters in `Install-Dagger` as unused but the script won't run if we delete
				// it.
				ExcludeRules: []string{"PSReviewUnusedParameter"},
			}).
			Assert(ctx)
	})
	return eg.Wait()
}

func (s Scripts) Test(ctx context.Context) error {
	ctr := dag.Alpine(
		dagger.AlpineOpts{
			Packages: []string{"curl"},
		}).
		Container().
		WithWorkdir("/opt/dagger").
		WithFile("/usr/local/bin/install.sh", s.Dagger.Source.File("install.sh"), dagger.ContainerWithFileOpts{
			Permissions: 0755,
		})

	eg := errgroup.Group{}

	test := func(name string, f func(ctx context.Context) error) {
		ctx, span := Tracer().Start(ctx, name)
		eg.Go(func() (rerr error) {
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
			return f(ctx)
		})
	}

	test("default install", func(ctx context.Context) error {
		ctr := ctr.
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "/opt/dagger/bin/dagger", nil)
	})

	test("install to location", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("BIN_DIR", "/opt/special-bin").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "/opt/special-bin/dagger", nil)
	})

	// install by semver
	test("install vX.Y.Z", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_VERSION", "v0.16.1").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.1"))
	})
	test("install vX.Y", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_VERSION", "v0.15").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.15.4"))
	})
	test("install X.Y.Z without v", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_VERSION", "0.16.1").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.1"))
	})

	// install latest
	test("install latest", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_VERSION", "latest").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", isVersion())
	})

	test("install git sha", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_COMMIT", "976cd0bf4be8d1cacbc3ee23a7ab057e8868ac2d").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.2-250227135944-976cd0bf4be8"))
	})
	test("install git head", func(ctx context.Context) error {
		ctr := ctr.
			WithEnvVariable("DAGGER_COMMIT", "head").
			WithExec([]string{"install.sh"})
		return checkDaggerVersion(ctx, ctr, "./bin/dagger", isVersion())
	})

	return eg.Wait()
}

func matchExactVersion(target string) func(string) error {
	return func(v string) error {
		if semver.Compare(target, v) != 0 {
			return fmt.Errorf(`expected %q to match semver %q`, v, target)
		}
		return nil
	}
}

func isVersion() func(string) error {
	return func(v string) error {
		if !semver.IsValid(v) {
			return fmt.Errorf(`expected %q to be valid semver`, v)
		}
		return nil
	}
}

func checkDaggerVersion(ctx context.Context, ctr *dagger.Container, path string, version func(string) error) error {
	out, err := ctr.
		WithExec([]string{path, "version"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	out = strings.TrimSpace(out)

	fields := strings.Fields(out)
	if fields[0] != "dagger" {
		return fmt.Errorf(`expected %q to contain "dagger"`, out)
	}
	if !semver.IsValid(fields[1]) {
		return fmt.Errorf(`expected %q to contain valid semver`, out)
	}
	if version != nil {
		if err := version(fields[1]); err != nil {
			return err
		}
	}
	currentPlatform := platforms.Format(platforms.DefaultSpec())
	if fields[len(fields)-1] != currentPlatform {
		return fmt.Errorf(`expected %q to contain the current platform %q`, out, currentPlatform)
	}

	return nil
}
