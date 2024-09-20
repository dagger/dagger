package main

import (
	"context"
	"dagger/release/internal/dagger"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/moby/buildkit/identity"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v2.2.0"
)

type Release struct {
	UnixInstallScript    *dagger.File      // +private
	WindowsInstallScript *dagger.File      // +private
	Tag                  string            // +private
	Commit               string            // +private
	ChangeNotes          *dagger.Directory // +private
}

func New(
	ctx context.Context,
	// +optional
	gitTag string,
	// +optional
	gitCommit string,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.git/HEAD", "!.git/refs", "!.git/config", "!.git/objects/*"]
	gitDir *dagger.Directory,
	// +optional
	// +defaultPath="./install.sh"
	unixInstallScript *dagger.File,
	// +optional
	// +defaultPath="./install.ps1"
	windowsInstallScript *dagger.File,
	// +optional
	// +defaultPath="./get-ref.sh"
	getRefScript *dagger.File,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.changes/*.md", "!**/.changes/*.md"]
	changeNotes *dagger.Directory,
) (Release, error) {
	// FIXME: get gitTag and gitCommit from gitDir
	return Release{
		UnixInstallScript:    unixInstallScript,
		WindowsInstallScript: windowsInstallScript,
		Tag:                  gitTag,
		Commit:               gitCommit,
		ChangeNotes:          changeNotes,
	}, nil
}

func git(workdir *dagger.Directory) *dagger.Container {
	return dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{"git"}}).
		WithMountedDirectory("/src", workdir).
		WithWorkdir("/src")
}

// Lint scripts files
func (r Release) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return dag.Shellcheck().
			Check(r.UnixInstallScript).
			Assert(ctx)
	})
	eg.Go(func() error {
		return dag.PsAnalyzer().
			Check(r.WindowsInstallScript, dagger.PsAnalyzerCheckOpts{
				// Exclude the unused parameters for now due because PSScriptAnalyzer treat
				// parameters in `Install-Dagger` as unused but the script won't run if we delete
				// it.
				ExcludeRules: []string{"PSReviewUnusedParameter"},
			}).
			Assert(ctx)
	})
	return eg.Wait()
}

// Test the release process
func (r Release) Test(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return dag.GoSDK().TestPublish(ctx, r.Tag)
	})
	eg.Go(func() error {
		// Test the release process of the Python SDK
		// FIXME: move this to ../sdk/python/dev
		return r.PublishPythonSDK(ctx, true, "", nil, "https://github.com/dagger/dagger.git", nil)
	})
	return eg.Wait()
}

// Verify that the CLI can be published, without actually publishing anything
func (r Release) TestPublishCli(ctx context.Context) error {
	// TODO: ideally this would also use go releaser, but we want to run this
	// step in PRs and locally and we use goreleaser pro features that require
	// a key which is private. For now, this just builds the CLI for the same
	// targets so there's at least some coverage
	eg, _ := errgroup.WithContext(context.Background())
	// Test that the goreleaser environment is not broken
	eg.Go(func() error {
		_, err := r.Goreleaser().Sync(ctx)
		return err
	})
	// Check that the build is not broken on any target platform
	for _, os := range []string{"linux", "windows", "darwin"} {
		for _, arch := range []string{"amd64", "arm64", "arm"} {
			if arch == "arm" && os == "darwin" {
				continue
			}
			platform := dagger.Platform(os + "/" + arch)
			if arch == "arm" {
				platform += "/v7" // not always correct but not sure of better way
			}
			eg.Go(func() error {
				// FIXME: we only need the engine builder, maybe spin that out
				goBase := dag.Engine(dagger.EngineOpts{
					Commit:   r.Commit,
					Tag:      r.Tag,
					Platform: platform,
				}).GoBase()
				_, err := dag.
					DaggerCli(dagger.DaggerCliOpts{
						Tag:    r.Tag,
						Commit: r.Commit,
						Base:   goBase,
					}).
					Binary(dagger.DaggerCliBinaryOpts{
						Platform: platform,
					}).
					Sync(ctx)
				return err
			})
		}
	}
	return eg.Wait()
}

// Build a Goreleaser environment
func (r Release) Goreleaser() *dagger.Container {
	return dag.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s-pro", goReleaserVersion)).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"}).
		// install nix
		WithExec([]string{"apk", "add", "xz"}).
		WithDirectory("/nix", dag.Directory()).
		WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
		WithExec([]string{"sh", "-c", "curl -fsSL https://nixos.org/nix/install | sh -s -- --no-daemon"}).
		WithEnvVariable("PATH", "$PATH:/nix/var/nix/profiles/default/bin",
			dagger.ContainerWithEnvVariableOpts{Expand: true},
		).
		// goreleaser requires nix-prefetch-url, so check we can run it
		WithExec([]string{"sh", "-c", "nix-prefetch-url 2>&1 | grep 'error: you must specify a URL'"}).
		WithWorkdir("/app")
}

// Publish the CLI using GoReleaser
func (r Release) PublishCli(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore_0.13=["!/cmd/dagger/*", "!**/go.sum", "!**/go.mod", "!**/*.go", "!.git", ".git/objects/*", "!.changes"]
	// stopgap:
	// +ignore=["bin", "**/node_modules", "**/.venv", "**/__pycache__"]
	source *dagger.Directory,
	// +optional
	version string,
	// +optional
	tag string,
	githubOrgName string,
	githubToken *dagger.Secret,
	goreleaserKey *dagger.Secret,
	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion *dagger.Secret,
	awsBucket *dagger.Secret,
	artefactsFQDN string,
) error {
	ctr := r.Goreleaser().WithMountedDirectory("", source)
	// Verify tag
	_, err := ctr.WithExec([]string{"git", "show-ref", "--verify", "refs/tags/" + tag}).Sync(ctx)
	if err != nil {
		err, ok := err.(*ExecError)
		if !ok || !strings.Contains(err.Stderr, "not a valid ref") {
			return err
		}
		// clear the set tag
		tag = ""
		// goreleaser refuses to run if there isn't a tag, so set it to a dummy but valid semver
		ctr = ctr.WithExec([]string{"git", "tag", "0.0.0"})
	}
	args := []string{"release", "--clean", "--skip=validate", "--verbose"}
	if tag != "" {
		args = append(args, "--release-notes", fmt.Sprintf(".changes/%s.md", tag))
	} else {
		// if this isn't an official semver version, do a dev release
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}
	_, err = ctr.
		WithEnvVariable("GH_ORG_NAME", githubOrgName).
		WithSecretVariable("GITHUB_TOKEN", githubToken).
		WithSecretVariable("GORELEASER_KEY", goreleaserKey).
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithSecretVariable("AWS_REGION", awsRegion).
		WithSecretVariable("AWS_BUCKET", awsBucket).
		WithEnvVariable("ARTEFACTS_FQDN", artefactsFQDN).
		WithEnvVariable("ENGINE_VERSION", version).
		WithEnvVariable("ENGINE_TAG", tag).
		WithEntrypoint([]string{"/sbin/tini", "--", "/entrypoint.sh"}).
		WithExec(args, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		Sync(ctx)
	return err
}

// Engine image targets to publish
var targets = []struct {
	Name       string
	Tag        string
	Distro     string // FIXME use Distro type from engine
	Platforms  []dagger.Platform
	GPUSupport bool
}{
	{
		Name:   "alpine (default)",
		Tag:    "%s",
		Distro: "alpine", // FIXME: reuse consts from engine module

		Platforms: []dagger.Platform{"linux/amd64", "linux/arm64"},
	},
	{
		Name:   "ubuntu with nvidia variant",
		Tag:    "%s-gpu",
		Distro: "ubuntu", // FIXME: reuse consts from engine module

		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
	{
		Name:   "wolfi",
		Tag:    "%s-wolfi",
		Distro: "wolfi", // FIXME: reuse consts from engine module

		Platforms: []dagger.Platform{"linux/amd64"},
	},
	{
		Name:       "wolfi with nvidia variant",
		Tag:        "%s-wolfi-gpu",
		Distro:     "wolfi", // FIXME: reuse consts from engine module
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
}

// Publish all engine images to a registry
func (r Release) PublishEngine(
	ctx context.Context,
	// Image target to push to
	image string,
	// List of tags to use
	tag []string,
	// +optional
	dryRun bool,
	// +optional
	registry *string,
	// +optional
	registryUsername *string,
	// +optional
	registryPassword *dagger.Secret,
) error {
	// collect all the targets that we are trying to build together, along with
	// where they need to go to
	targetResults := make([]struct {
		Platforms []*dagger.Container
		Tags      []string
	}, len(targets))
	eg, egCtx := errgroup.WithContext(ctx)
	for i, target := range targets {
		// determine the target tags
		for _, tag := range tag {
			targetResults[i].Tags = append(targetResults[i].Tags, fmt.Sprintf(target.Tag, tag))
		}
		// build all the target platforms
		targetResults[i].Platforms = make([]*dagger.Container, len(target.Platforms))
		for j, platform := range target.Platforms {
			i, j := i, j // https://golang.org/doc/faq#closures_and_goroutines
			egCtx, span := Tracer().Start(egCtx, fmt.Sprintf("building %s [%s]", target.Name, platform))
			eg.Go(func() (rerr error) {
				defer func() {
					if rerr != nil {
						span.SetStatus(codes.Error, rerr.Error())
					}
					span.End()
				}()
				ctr, err := dag.
					Engine(dagger.EngineOpts{
						Gpu:      target.GPUSupport,
						Distro:   target.Distro,
						Platform: platform,
					}).
					Container(dagger.EngineContainerOpts{
						Scan: true, // Scan before releasing
					}).
					// Make sure all containers build before pushing anything
					Sync(egCtx)
				if err != nil {
					return err
				}
				targetResults[i].Platforms[j] = ctr
				return nil
			})
		}
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	// push all the targets
	ctr := dag.Container()
	if registry != nil && registryUsername != nil && registryPassword != nil {
		ctr = ctr.WithRegistryAuth(*registry, *registryUsername, registryPassword)
	}
	for i, target := range targets {
		result := targetResults[i]

		if err := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("pushing %s", target.Name))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()

			for _, tag := range result.Tags {
				_, err := ctr.
					Publish(ctx, fmt.Sprintf("%s:%s", image, tag), dagger.ContainerPublishOpts{
						PlatformVariants:  result.Platforms,
						ForcedCompression: dagger.Gzip, // use gzip to avoid incompatibility w/ older docker versions
					})
				if err != nil {
					return err
				}
			}
			return nil
		}(); err != nil {
			return err
		}
	}

	return nil
}

// Publish the Python SDK
// FIXME: move this to ../sdk/python/dev
func (r Release) PublishPythonSDK(
	ctx context.Context,

	// +optional
	dryRun bool,

	// +optional
	pypiRepo string,
	// +optional
	pypiToken *dagger.Secret,

	// +optional
	// +default="https://github.com/dagger/dagger.git"
	gitRepoSource string,
	// +optional
	githubToken *dagger.Secret,
) error {
	version, isVersioned := strings.CutPrefix(r.Tag, "sdk/python/")
	if dryRun {
		version = "v0.0.0"
	}
	if pypiRepo == "" || pypiRepo == "pypi" {
		pypiRepo = "main"
	}

	// TODO: move this to PythonSDKDev
	result := dag.PythonSDKDev().
		Container().
		WithEnvVariable("SETUPTOOLS_SCM_PRETEND_VERSION", strings.TrimPrefix(version, "v")).
		WithEnvVariable("HATCH_INDEX_REPO", pypiRepo).
		WithEnvVariable("HATCH_INDEX_USER", "__token__").
		WithExec([]string{"uvx", "hatch", "build"})
	if !dryRun {
		result = result.
			WithSecretVariable("HATCH_INDEX_AUTH", pypiToken).
			WithExec([]string{"uvx", "hatch", "publish"})
	}
	_, err := result.Sync(ctx)
	if err != nil {
		return err
	}
	if isVersioned {
		return r.GithubRelease(
			ctx,
			r.Tag,
			r.changeNotesFile("sdk/python", version),
			gitRepoSource,
			githubToken,
			dryRun,
		)
	}
	return nil
}

func (r Release) changeNotesFile(component, version string) *dagger.File {
	return r.ChangeNotes.File(fmt.Sprintf("%s/.changes/%s.md", component, version))
}

// Publish an SDK to a git repository
func (r Release) GitPublish(
	ctx context.Context,
	// Source repository URL
	// +optional
	source string,
	// Destination repository URL
	// +optional
	dest string,
	// Tag or reference in the source repository
	// +optional
	sourceTag string,
	// Tag or reference in the destination repository
	// +optional
	destTag string,
	// Path within the source repository to publish
	// +optional
	sourcePath string,
	// Filter to apply to the source files
	// +optional
	sourceFilter string,
	// Container environment for source operations
	// +optional
	sourceEnv *dagger.Container,
	// Git username for commits
	// +optional
	username string,
	// Git email for commits
	// +optional
	email string,
	// GitHub token for authentication
	// +optional
	githubToken *dagger.Secret,
	// Whether to perform a dry run without pushing changes
	// +optional
	dryRun bool,
) error {
	base := sourceEnv
	if base == nil {
		base = dag.Wolfi().
			Container(dagger.WolfiContainerOpts{
				Packages: []string{
					"git",
					"go",
					"python3",
				},
			})
	}
	// FIXME: move this into std modules
	git := base.
		WithExec([]string{"git", "config", "--global", "user.name", username}).
		WithExec([]string{"git", "config", "--global", "user.email", email})
	if !dryRun {
		githubTokenRaw, err := githubToken.Plaintext(ctx)
		if err != nil {
			return err
		}
		encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + githubTokenRaw))
		git = git.
			WithEnvVariable("GIT_CONFIG_COUNT", "1").
			WithEnvVariable("GIT_CONFIG_KEY_0", "http.https://github.com/.extraheader").
			WithSecretVariable("GIT_CONFIG_VALUE_0", dag.SetSecret("GITHUB_HEADER", fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT)))
	}

	result := git.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithWorkdir("/src/dagger").
		WithExec([]string{"git", "clone", source, "."}).
		WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(sourceTag, "refs/"))}).
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{
			"git", "filter-branch", "-f", "--prune-empty",
			"--subdirectory-filter", sourcePath,
			"--tree-filter", sourceFilter,
			"--", sourceTag,
		})
	if !dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			// "--force", // NOTE: disabled to avoid accidentally rewriting the history
			dest,
			fmt.Sprintf("%s:%s", sourceTag, destTag),
		})
	} else {
		// on a dry run, just test that the last state of dest is in the current branch (and is a fast-forward)
		history, err := result.
			WithExec([]string{"git", "log", "--oneline", "--no-abbrev-commit", sourceTag}).
			Stdout(ctx)
		if err != nil {
			return err
		}

		destCommit, err := git.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithWorkdir("/src/dagger").
			WithExec([]string{"git", "clone", dest, "."}).
			WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(destTag, "refs/"))}).
			WithExec([]string{"git", "checkout", destTag, "--"}).
			WithExec([]string{"git", "rev-parse", "HEAD"}).
			Stdout(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "invalid reference: "+destTag) {
				// this is a ref that only exists in the source, and not in the
				// dest, so no overwriting will occur
				return nil
			}
			return err
		}
		destCommit = strings.TrimSpace(destCommit)

		if !strings.Contains(history, destCommit) {
			return fmt.Errorf("publish would rewrite history - %s not found\n%s", destCommit, history)
		}
		return nil
	}

	_, err := result.Sync(ctx)
	return err
}

// Publish a Github release
func (r Release) GithubRelease(
	ctx context.Context,
	// Tag for the GitHub release
	// +optional
	tag string,
	// File containing release notes
	// +optional
	notes *dagger.File,
	// GitHub repository URL
	// +optional
	gitRepo string,
	// GitHub token for authentication
	// +optional
	githubToken *dagger.Secret,
	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	u, err := url.Parse(gitRepo)
	if err != nil {
		return err
	}
	if u.Host != "github.com" {
		return fmt.Errorf("git repo must be on github.com")
	}
	githubRepo := strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/")

	if dryRun {
		// sanity check tag is in target repo
		_, err = dag.
			Git(fmt.Sprintf("https://github.com/%s", githubRepo)).
			Ref(tag).
			Tree().
			Sync(ctx)
		if err != nil {
			return err
		}

		// sanity check notes file exists
		notesContent, err := notes.Contents(ctx)
		if err != nil {
			return err
		}
		fmt.Println(notesContent)

		return nil
	}

	gh := dag.Gh(dagger.GhOpts{
		Repo:  githubRepo,
		Token: githubToken,
	})
	return gh.Release().Create(
		ctx,
		tag,
		tag,
		dagger.GhReleaseCreateOpts{
			VerifyTag: true,
			Draft:     true,
			NotesFile: notes,
			// Latest:    false,  // can't do this yet
		},
	)
}
