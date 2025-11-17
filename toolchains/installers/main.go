package main

import (
	"context"
	"fmt"
	"strings"

	"toolchains/installers/internal/dagger"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/util/parallel"
	"golang.org/x/mod/semver"
)

// A toolchain to test Dagger installers
type Installers struct{}

// Lint install bash script
// +check
func (Installers) LintBashScript(
	ctx context.Context,
	// +defaultPath="/install.sh"
	installShellScript *dagger.File,
) error {
	return dag.Shellcheck().
		Check(installShellScript).
		Assert(ctx)
}

// LintPowershell scripts files
// +check
func (Installers) LintPowershellScript(
	ctx context.Context,
	// +defaultPath="/install.ps1"
	powershellScript *dagger.File,
) error {
	return dag.PsAnalyzer().
		Check(powershellScript, dagger.PsAnalyzerCheckOpts{
			// Exclude the unused parameters for now due because PSScriptAnalyzer treat
			// parameters in `Install-Dagger` as unused but the script won't run if we delete
			// it.
			ExcludeRules: []string{"PSReviewUnusedParameter"},
		}).
		Assert(ctx)
}

// Test install bash script
// +check
func (Installers) TestBashScript(
	ctx context.Context,
	// +defaultPath="/install.sh"
	installShellScript *dagger.File,
) error {
	ctr := dag.Alpine(
		dagger.AlpineOpts{
			Packages: []string{"curl"},
		}).
		Container().
		WithWorkdir("/opt/dagger").
		WithFile("/usr/local/bin/install.sh", installShellScript, dagger.ContainerWithFileOpts{
			Permissions: 0755,
		})
	return parallel.New().
		WithJob("default install", func(ctx context.Context) error {
			ctr := ctr.
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "/opt/dagger/bin/dagger", nil)
		}).
		WithJob("install to location", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("BIN_DIR", "/opt/special-bin").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "/opt/special-bin/dagger", nil)
		}).
		WithJob("install vX.Y.Z", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_VERSION", "v0.16.1").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.1"))
		}).
		WithJob("install vX.Y", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_VERSION", "v0.15").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.15.4"))
		}).
		WithJob("install X.Y.Z without v", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_VERSION", "0.16.1").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.1"))
		}).
		WithJob("install latest", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_VERSION", "latest").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", isVersion())
		}).
		WithJob("install git sha", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_COMMIT", "976cd0bf4be8d1cacbc3ee23a7ab057e8868ac2d").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", matchExactVersion("v0.16.2-250227135944-976cd0bf4be8"))
		}).
		WithJob("install git head", func(ctx context.Context) error {
			ctr := ctr.
				WithEnvVariable("DAGGER_COMMIT", "head").
				WithExec([]string{"install.sh"})
			return checkDaggerVersion(ctx, ctr, "./bin/dagger", isVersion())
		}).
		Run(ctx)
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
