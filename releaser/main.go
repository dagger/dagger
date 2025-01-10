// A module that encodes the official release process of the Dagger Engine
package main

import (
	"bytes"
	"context"
	"dagger/releaser/internal/dagger"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

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
func (r *Releaser) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	dir := dag.Directory().
		WithDirectory("", r.Dagger.SDK().All().Bump(version)).
		WithDirectory("", r.Dagger.Docs().Bump(version)).
		WithFile("helm/dagger/Chart.yaml", dag.Helm().SetVersion(version))
	return dir, nil
}

type ReleaseReport struct {
	Ref     string
	Commit  string
	Version string

	Date string

	Artifacts []ReleaseReportArtifact
	FollowUps []ReleaseReportFollowUp
}

type ReleaseReportArtifact struct {
	Name      string
	Tag       string
	Changelog *dagger.File
	Link      string

	Notify bool // +private
}

type ReleaseReportFollowUp struct {
	Name string
	Link string
}

//go:embed report.md.tmpl
var reportTmpl string

func (report *ReleaseReport) Markdown() (string, error) {
	tmpl, err := template.New("").Parse(reportTmpl)
	if err != nil {
		return "", err
	}

	var result bytes.Buffer
	err = tmpl.ExecuteTemplate(&result, "", report)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}

func (report *ReleaseReport) Notify(
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

func (r *Releaser) Publish(
	ctx context.Context,
	tag string,
	commit string,

	registryUsername string,
	registryPassword *dagger.Secret,

	goreleaserKey *dagger.Secret,

	githubToken *dagger.Secret,
	githubOrgName string,

	netlifyToken *dagger.Secret,
	pypiToken *dagger.Secret,
	npmToken *dagger.Secret,
	hexAPIKey *dagger.Secret,
	cargoRegistryToken *dagger.Secret,

	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion string,
	awsBucket string,
	awsCloudFrontDistribution string,
	artefactsFQDN string,
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

	err := r.Dagger.Engine().Publish(ctx, []string{tag, commit}, dagger.DaggerDevDaggerEnginePublishOpts{
		RegistryUsername: registryUsername,
		RegistryPassword: registryPassword,
	})
	if err != nil {
		return nil, err
	}
	report.Artifacts = append(report.Artifacts, ReleaseReportArtifact{
		Name:      "🚙 Engine",
		Tag:       tag,
		Changelog: r.changeNotes("", version),
		Notify:    true,
	})

	err = r.Dagger.Cli().Publish(ctx, tag, githubOrgName, githubToken, goreleaserKey, awsAccessKeyID, awsSecretAccessKey, awsRegion, awsBucket, artefactsFQDN)
	if err != nil {
		return nil, err
	}
	err = r.Dagger.Cli().PublishMetadata(ctx, awsAccessKeyID, awsSecretAccessKey, awsRegion, awsBucket, awsCloudFrontDistribution)
	if err != nil {
		return nil, err
	}
	report.Artifacts = append(report.Artifacts, ReleaseReportArtifact{
		Name:      "🚗 CLI",
		Tag:       tag,
		Changelog: r.changeNotes("", version),
	})

	if semver.IsValid(version) {
		err = r.Dagger.Docs().Publish(ctx, netlifyToken)
		if err != nil {
			return nil, err
		}

		report.Artifacts = append(report.Artifacts, ReleaseReportArtifact{
			Name: "📖 Docs",
			Link: "https://docs.dagger.io",
		})
	}

	if semver.IsValid(version) {
		components := []struct {
			name    string
			path    string
			tag     string
			link    string
			publish func() error
		}{
			{
				name: "🐹 Go SDK",
				path: "sdk/go/",
				tag:  "sdk/go/",
				link: "https://pkg.go.dev/dagger.io/dagger@" + version,
				publish: func() error {
					return r.Dagger.SDK().Go().Publish(ctx, tag, dagger.DaggerDevGoSDKPublishOpts{
						GithubToken: githubToken,
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
					})
				},
			},
			{
				name: "⚙️ Rust SDK",
				path: "sdk/rust/",
				tag:  "sdk/rust/",
				link: "https://crates.io/crates/dagger-sdk/" + version,
				publish: func() error {
					return r.Dagger.SDK().Rust().Publish(ctx, tag, dagger.DaggerDevRustSDKPublishOpts{
						CargoRegistryToken: cargoRegistryToken,
					})
				},
			},
			{
				name: "🐘 PHP SDK",
				path: "sdk/php/",
				tag:  "sdk/php/",
				link: "https://packagist.org/packages/dagger/dagger#" + version,
				publish: func() error {
					return r.Dagger.SDK().Php().Publish(ctx, tag, dagger.DaggerDevPhpsdkPublishOpts{
						GithubToken: githubToken,
					})
				},
			},
			{
				name: "☸️ Helm Chart",
				path: "helm/dagger/",
				tag:  "helm/chart/",
				link: "https://github.com/dagger/dagger/pkgs/container/dagger-helm",
				publish: func() error {
					return r.Dagger.SDK().Php().Publish(ctx, tag, dagger.DaggerDevPhpsdkPublishOpts{
						GithubToken: githubToken,
					})
				},
			},
		}

		artifacts := make([]ReleaseReportArtifact, len(components))
		var eg errgroup.Group
		for i, component := range components {
			eg.Go(func() error {
				if err := component.publish(); err != nil {
					return err
				}

				target := strings.TrimSuffix(component.tag, "/") + "/" + version
				notes := r.changeNotes(component.path, version)
				if err := r.githubRelease(ctx, "https://github.com/dagger/dagger", tag, target, notes, githubToken, false); err != nil {
					return err
				}

				artifacts[i] = ReleaseReportArtifact{
					Name:      component.name,
					Tag:       target,
					Changelog: notes,
					Link:      component.link,
					Notify:    true,
				}

				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		report.Artifacts = append(report.Artifacts, artifacts...)
	}

	if semver.IsValid(version) {
		report.FollowUps = append(report.FollowUps, ReleaseReportFollowUp{
			Name: "❄️ Nix",
			Link: "https://github.com/dagger/nix",
		})

		report.FollowUps = append(report.FollowUps, ReleaseReportFollowUp{
			Name: "🍺 Homebrew Tap",
			Link: "https://github.com/dagger/homebrew-tap",
		})
		report.FollowUps = append(report.FollowUps, ReleaseReportFollowUp{
			Name: "🍺 Homebrew Core",
			Link: "https://github.com/Homebrew/homebrew-core/pulls?q=is%3Apr+in%3Atitle+dagger+" + strings.TrimPrefix(version, "v"),
		})

		report.FollowUps = append(report.FollowUps, ReleaseReportFollowUp{
			Name: "🌌 Daggerverse",
			Link: "https://github.com/dagger/dagger.io/pulls?q=author%3Adagger-ci+is%3Apr+in%3Atitle+dgvs+" + strings.TrimPrefix(version, "v"),
		})
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
