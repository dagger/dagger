package main

import (
	"fmt"

	"github.com/dagger/dagger/dev/goreleaser/internal/dagger"
)

func New(
	// Fully Qualified Domain Name where artifacts should be published.
	// This is a useful configuration on top of goreleaser, not a core feature
	// +optional
	fqdn string,
	// Select a goreleaser version
	//  See https://github.com/goreleaser/goreleaser/releases
	// +optional
	// +default="v1.26.0"
	version string,
) Goreleaser {
	return GoReleaser{
		FQDN:    fqdn,
		Version: version,
	}
}

type Goreleaser struct {
	// Nix customization
	Nix bool
	// Fully Qualified Domain Name where artifacts should be published.
	FQDN string
	// Goreleaser version
	Version string
	// AWS configuration
	AWS *AWSConfig
	// Github configuration
	Github *GithubConfig
	// Goreleaser configuration file
	ConfigFile *dagger.File
}

// Configure AWS
func (m Goreleaser) WithAWS(region, bucket, ID string, secret *dagger.Secret) Goreleaser {
	m.AWS = &AWSConfig{
		Region: region,
		Bucket: bucket,
		ID:     ID,
		Secret: secret,
	}
	return m
}

type AWSConfig struct {
	// AWS Access Key ID
	ID string
	// AWS Secret Access Key
	Secret *dagger.Secret
	// AWS Region
	Region string
	// AWS S3 bucket
	Bucket string
}

func (m Goreleaser) WithGithub(org string, token *dagger.Secret) Goreleaser {
	m.Github = &GithubConfig{
		Org:   org,
		Token: token,
	}
	return m
}

type GithubConfig struct {
	// Github organization name
	Org string
	// Github API token
	Token *dagger.Secret
}

// Activate Nix customization
// https://goreleaser.com/customization/nix/
func (m Goreleaser) WithNix() GoReleaser {
	m.Nix = true
	return m
}

// Build a container ready to execute goreleaser and its customizations
func (m *Goreleaser) Container() *dagger.Container {
	ctr := dag.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s-pro", m.Version)).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"})
	if m.Nix {
		// install nix
		ctr = ctr.
			WithExec([]string{"apk", "add", "xz"}).
			WithDirectory("/nix", dag.Directory()).
			WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
			WithExec([]string{"sh", "-c", "curl -L https://nixos.org/nix/install | sh -s -- --no-daemon"}).
			WithEnvVariable(
				"PATH",
				"${PATH}:/nix/var/nix/profiles/default/bin",
				dagger.ContainerWithEnvVariableOpts{Expand: true},
			).
			// goreleaser requires nix-prefetch-url, so check we can run it
			WithExec([]string{"sh", "-c", "nix-prefetch-url 2>&1 | grep 'error: you must specify a URL'"})
	}
	return ctr
}
