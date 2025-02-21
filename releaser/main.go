// A module that encodes the official release process of the Dagger Engine
package main

import (
	"bytes"
	"cmp"
	"context"
	"dagger/releaser/internal/dagger"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	sprig "github.com/go-task/slim-sprig/v3"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

type Releaser struct {
	Dagger *dagger.DaggerDev // +private
}

func New(
	// +optional
	source *dagger.Directory,
	// +optional
	dockerCfg *dagger.Secret,
) Releaser {
	return Releaser{
		Dagger: dag.DaggerDev(dagger.DaggerDevOpts{
			Source:    source,
			DockerCfg: dockerCfg,
		}),
	}
}

// Bump the engine version used by all SDKs and the Helm chart
func (r *Releaser) Bump(version string) *dagger.Directory {
	return dag.Directory().
		WithDirectory("", r.Dagger.SDK().All().Bump(version)).
		WithDirectory("", r.Dagger.Docs().Bump(version)).
		WithFile("helm/dagger/Chart.yaml", dag.Helm().SetVersion(version))
}

type ReleaseReport struct {
	Ref     string
	Commit  string
	Version string

	Date string

	Artifacts []*ReleaseReportArtifact
	FollowUps []*ReleaseReportFollowUp

	Errors []*dagger.Error
}

type ReleaseReportArtifact struct {
	Name string
	Tag  string
	Link string

	Errors []*dagger.Error

	Notify bool // +private
}

type ReleaseReportFollowUp struct {
	Name string
	Link string
}

//go:embed report.md.tmpl
var reportTmpl string

func (report *ReleaseReport) Markdown(ctx context.Context) (string, error) {
	tmpl, err := template.New("").Funcs(sprig.FuncMap()).Parse(reportTmpl)
	if err != nil {
		return "", err
	}

	var result bytes.Buffer
	err = tmpl.ExecuteTemplate(&result, "", struct {
		*ReleaseReport
		Context context.Context
	}{report, ctx})
	if err != nil {
		return "", err
	}
	return result.String(), nil
}

func (report *ReleaseReport) notify(
	ctx context.Context,
	discordWebhook *dagger.Secret,
) error {
	for _, artifact := range report.Artifacts {
		if !artifact.Notify {
			continue
		}

		message := fmt.Sprintf("%s: https://github.com/dagger/dagger/releases/tag/%s", artifact.Name, artifact.Tag)
		_, err := dag.Notify().Discord(ctx, discordWebhook, message)
		if err != nil {
			return err
		}
	}

	return nil
}

func (report *ReleaseReport) hasErrors() bool {
	if len(report.Errors) > 0 {
		return true
	}
	for _, artifact := range report.Artifacts {
		if len(artifact.Errors) > 0 {
			return true
		}
	}
	return false
}

func (r *Releaser) Publish(
	ctx context.Context,
	tag string,
	commit string,

	dryRun bool, // +optional

	registryImage string, // +optional
	registryUsername string, // +optional
	registryPassword *dagger.Secret, // +optional

	goreleaserKey *dagger.Secret, // +optional

	githubToken *dagger.Secret, // +optional
	githubOrgName string, // +optional

	netlifyToken *dagger.Secret, // +optional
	pypiToken *dagger.Secret, // +optional
	pypiRepo string, // +optional
	npmToken *dagger.Secret, // +optional
	hexAPIKey *dagger.Secret, // +optional
	cargoRegistryToken *dagger.Secret, // +optional

	awsAccessKeyID *dagger.Secret, // +optional
	awsSecretAccessKey *dagger.Secret, // +optional
	awsRegion string, // +optional
	awsBucket string, // +optional
	awsCloudfrontDistribution string, // +optional
	artefactsFQDN string, // +optional

	discordWebhook *dagger.Secret, // +optional
) (*ReleaseReport, error) {
	version := ""
	if semver.IsValid(tag) {
		version = tag
	}

	report := ReleaseReport{
		Date:    time.Now().UTC().Format(time.RFC822),
		Ref:     tag,
		Commit:  commit,
		Version: version,
	}

	artifact := &ReleaseReportArtifact{
		Name:   "🚙 Engine",
		Tag:    tag,
		Notify: true,
	}

	tags := []string{tag, commit}
	if semver.IsValid(version) && semver.Prerelease(version) == "" {
		// this is a public release
		tags = append(tags, "latest")
	}
	err := r.Dagger.Engine().Publish(ctx, tags, dagger.DaggerDevDaggerEnginePublishOpts{
		Image:            registryImage,
		RegistryUsername: registryUsername,
		RegistryPassword: registryPassword,
		DryRun:           dryRun,
	})
	if err != nil {
		artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
	}
	report.Artifacts = append(report.Artifacts, artifact)

	artifact = &ReleaseReportArtifact{
		Name: "🚗 CLI",
		Tag:  tag,
	}
	if !dryRun {
		_, err := r.Dagger.Cli().
			Publish(tag, goreleaserKey, githubOrgName, dagger.DaggerDevCliPublishOpts{
				GithubToken:        githubToken,
				AwsAccessKeyID:     awsAccessKeyID,
				AwsSecretAccessKey: awsSecretAccessKey,
				AwsRegion:          awsRegion,
				AwsBucket:          awsBucket,
				ArtefactsFqdn:      artefactsFQDN,
			}).
			Sync(ctx)
		if err != nil {
			artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
		}
		err = r.Dagger.Cli().PublishMetadata(ctx, awsAccessKeyID, awsSecretAccessKey, awsRegion, awsBucket, awsCloudfrontDistribution)
		if err != nil {
			artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
		}
	} else {
		err = r.Dagger.Cli().TestPublish(ctx)
		if err != nil {
			artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
		}
	}
	report.Artifacts = append(report.Artifacts, artifact)

	if report.hasErrors() {
		// early-exit if engine / cli could not Publish
		return &report, nil
	}

	isPrerelease := semver.IsValid(version) && semver.Prerelease(version) != ""
	if isPrerelease {
		// early-exit if this is a pre-release
		return &report, nil
	}

	if semver.IsValid(version) {
		artifact = &ReleaseReportArtifact{
			Name: "📖 Docs",
			Link: "https://docs.dagger.io",
		}
		if !dryRun {
			err = r.Dagger.Docs().Publish(ctx, netlifyToken)
			if err != nil {
				artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
			}
		}
		report.Artifacts = append(report.Artifacts, artifact)
	}

	components := []struct {
		name    string
		path    string
		tag     string
		link    string
		dev     bool
		publish func() error
	}{
		{
			name: "🐹 Go SDK",
			path: "sdk/go/",
			tag:  "sdk/go/",
			link: "https://pkg.go.dev/dagger.io/dagger@" + cmp.Or(version, "main"),
			dev:  true,
			publish: func() error {
				return r.Dagger.SDK().Go().Publish(ctx, tag, dagger.DaggerDevGoSDKPublishOpts{
					GithubToken: githubToken,
					DryRun:      dryRun,
				})
			},
		},
		{
			name: "🐍 Python SDK",
			path: "sdk/python/",
			tag:  "sdk/python/",
			link: "https://pypi.org/project/dagger-io/" + strings.TrimPrefix(version, "v"),
			publish: func() error {
				return r.Dagger.SDK().Python().Publish(ctx, tag, dagger.DaggerDevPythonSDKPublishOpts{
					PypiToken: pypiToken,
					PypiRepo:  pypiRepo,
					DryRun:    dryRun,
				})
			},
		},
		{
			name: "⬢ TypeScript SDK",
			path: "sdk/typescript/",
			tag:  "sdk/typescript/",
			link: "https://www.npmjs.com/package/@dagger.io/dagger/v/" + strings.TrimPrefix(version, "v"),
			publish: func() error {
				return r.Dagger.SDK().Typescript().Publish(ctx, tag, dagger.DaggerDevTypescriptSDKPublishOpts{
					NpmToken: npmToken,
					DryRun:   dryRun,
				})
			},
		},
		{
			name: "🧪 Elixir SDK",
			path: "sdk/elixir/",
			tag:  "sdk/elixir/",
			link: "https://hex.pm/packages/dagger/" + strings.TrimPrefix(version, "v"),
			publish: func() error {
				return r.Dagger.SDK().Elixir().Publish(ctx, tag, dagger.DaggerDevElixirSDKPublishOpts{
					HexApikey: hexAPIKey,
					DryRun:    dryRun,
				})
			},
		},
		{
			name: "⚙️ Rust SDK",
			path: "sdk/rust/",
			tag:  "sdk/rust/",
			link: "https://crates.io/crates/dagger-sdk/" + strings.TrimPrefix(version, "v"),
			publish: func() error {
				return r.Dagger.SDK().Rust().Publish(ctx, tag, dagger.DaggerDevRustSDKPublishOpts{
					CargoRegistryToken: cargoRegistryToken,
					DryRun:             dryRun,
				})
			},
		},
		{
			name: "🐘 PHP SDK",
			path: "sdk/php/",
			tag:  "sdk/php/",
			link: "https://packagist.org/packages/dagger/dagger#" + cmp.Or(version, "dev-main"),
			dev:  true,
			publish: func() error {
				return r.Dagger.SDK().Php().Publish(ctx, tag, dagger.DaggerDevPhpsdkPublishOpts{
					GithubToken: githubToken,
					DryRun:      dryRun,
				})
			},
		},
		{
			name: "☸️ Helm Chart",
			path: "helm/dagger/",
			tag:  "helm/chart/",
			link: "https://github.com/dagger/dagger/pkgs/container/dagger-helm",
			publish: func() error {
				return dag.Helm().Publish(ctx, tag, dagger.HelmPublishOpts{
					GithubToken: githubToken,
					DryRun:      dryRun,
				})
			},
		},
	}
	artifacts := make([]*ReleaseReportArtifact, len(components))
	var eg errgroup.Group
	for i, component := range components {
		if component.dev || semver.IsValid(version) {
			eg.Go(func() error {
				target := ""
				if semver.IsValid(version) {
					target = strings.TrimSuffix(component.tag, "/") + "/" + version
				}

				artifact := &ReleaseReportArtifact{
					Name:   component.name,
					Tag:    target,
					Link:   component.link,
					Notify: true,
				}
				artifacts[i] = artifact

				if err := component.publish(); err != nil {
					artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
					return nil
				}

				if semver.IsValid(version) {
					notes := r.changeNotes(component.path, version)
					if err := r.githubRelease(ctx, "https://github.com/dagger/dagger", tag, target, notes, githubToken, dryRun); err != nil {
						artifact.Errors = append(artifact.Errors, dag.Error(err.Error()))
						return nil
					}
				}

				return nil
			})
		}
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		report.Artifacts = append(report.Artifacts, artifact)
	}

	if semver.IsValid(version) {
		report.FollowUps = append(report.FollowUps, &ReleaseReportFollowUp{
			Name: "❄️ Nix",
			Link: "https://github.com/dagger/nix",
		})

		report.FollowUps = append(report.FollowUps, &ReleaseReportFollowUp{
			Name: "🍺 Homebrew Tap",
			Link: "https://github.com/dagger/homebrew-tap",
		})
		report.FollowUps = append(report.FollowUps, &ReleaseReportFollowUp{
			Name: "🍺 Homebrew Core",
			Link: "https://github.com/Homebrew/homebrew-core/pulls?q=is%3Apr+in%3Atitle+dagger+" + strings.TrimPrefix(version, "v"),
		})

		report.FollowUps = append(report.FollowUps, &ReleaseReportFollowUp{
			Name: "🌌 Daggerverse",
			Link: "https://github.com/dagger/dagger.io/pulls?q=author%3Adagger-ci+is%3Apr+in%3Atitle+dgvs+" + strings.TrimPrefix(version, "v"),
		})
	}

	if semver.IsValid(version) && discordWebhook != nil {
		if err := report.notify(ctx, discordWebhook); err != nil {
			report.Errors = append(report.Errors, dag.Error(err.Error()))
		}
	}

	return &report, nil
}

func (r Releaser) Notify(
	ctx context.Context,
	// GitHub repository URL
	repository string,
	// The target tag for the release
	// e.g. sdk/typescript/v0.14.0
	target string,
	// Name of the component to release
	name string,
	// Discord webhook
	// +optional
	discordWebhook *dagger.Secret,

	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	githubRepo, err := githubRepo(repository)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	if discordWebhook != nil {
		message := fmt.Sprintf("%s: https://github.com/%s/releases/tag/%s", name, githubRepo, target)
		_, err = dag.Notify().Discord(ctx, discordWebhook, message)
		if err != nil {
			return err
		}
	}

	return nil
}
