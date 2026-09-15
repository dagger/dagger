// GitHub CLI
package main

import (
	"errors"
	"strings"
	"time"

	"dagger/gh/internal/dagger"
)

type Gh struct {
	// GitHub token.
	//
	// +private
	Token *dagger.Secret

	// GitHub repository (e.g. "owner/repo").
	//
	// +private
	Repository string

	// GitHub host.
	//
	// +private
	Host string

	// Additional CA certificate for the GitHub host.
	//
	// +private
	CACert *dagger.File

	// Git repository source (with .git directory).
	Source *dagger.Directory
}

func New(
	// GitHub token.
	//
	// +optional
	token *dagger.Secret,

	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,

	// GitHub host.
	//
	// +optional
	host string,

	// Additional CA certificate for the GitHub host.
	//
	// +optional
	caCert *dagger.File,

	// Git repository source (with .git directory).
	//
	// +optional
	source *dagger.Directory,
) *Gh {
	return &Gh{
		Token:      token,
		Repository: repo,
		Host:       host,
		CACert:     caCert,
		Source:     source,
	}
}

// Set a GitHub token.
func (m *Gh) WithToken(
	// GitHub token.
	token *dagger.Secret,
) *Gh {
	gh := *m

	gh.Token = token

	return &gh
}

// Set a GitHub repository as context.
func (m *Gh) WithRepo(
	// GitHub repository (e.g. "owner/repo").
	repo string,
) (*Gh, error) {
	gh := *m

	gh.Repository = repo

	return &gh, nil
}

// Set a GitHub host as context.
func (m *Gh) WithHost(
	// GitHub host.
	host string,
) *Gh {
	gh := *m

	gh.Host = host

	return &gh
}

// Set an additional CA certificate for the GitHub host.
func (m *Gh) WithCACert(
	// Additional CA certificate for the GitHub host.
	caCert *dagger.File,
) *Gh {
	gh := *m

	gh.CACert = caCert

	return &gh
}

// Load a Git repository source (with .git directory).
func (m *Gh) WithSource(
	// Git repository source (with .git directory).
	source *dagger.Directory,
) *Gh {
	gh := *m

	gh.Source = source

	return &gh
}

// Clone a GitHub repository.
func (m *Gh) Clone(
	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,
) (*Gh, error) {
	if repo == "" {
		repo = m.Repository
	}

	if repo == "" {
		return nil, errors.New("no repository specified")
	}

	return m.WithSource(m.Repo().Clone(repo, nil, nil)), nil
}

// Run a GitHub CLI command (accepts a single command string without "gh").
func (m *Gh) Run(
	// Command to run.
	cmd string,

	// GitHub token.
	//
	// +optional
	token *dagger.Secret,

	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,
) *dagger.Container {
	return m.container(token, repo).WithExec([]string{"sh", "-c", strings.Join([]string{"gh", cmd}, " ")})
}

// Run a GitHub CLI command (accepts a list of arguments without "gh").
func (m *Gh) Exec(
	// Arguments to pass to GitHub CLI.
	args []string,

	// GitHub token.
	//
	// +optional
	token *dagger.Secret,

	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,
) *dagger.Container {
	args = append([]string{"gh"}, args...)

	return m.container(token, repo).WithExec(args)
}

// Run a git command (accepts a list of arguments without "git").
func (m *Gh) WithGitExec(
	// Arguments to pass to GitHub CLI.
	args []string,
) (*Gh, error) {
	if m.Source == nil {
		return nil, errors.New("no git repository available")
	}

	args = append([]string{"git"}, args...)

	return m.WithSource(m.container(nil, "").WithExec(args).Directory("/work/repo")), nil
}

// Open an interactive terminal.
func (m *Gh) Terminal(
	// GitHub token.
	//
	// +optional
	token *dagger.Secret,

	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,
) *dagger.Container {
	return m.container(token, repo).Terminal()
}

func (m *Gh) host() string {
	if m.Host != "" {
		return m.Host
	}
	return "github.com"
}

func (m *Gh) base() *dagger.Container {
	host := m.host()
	ctr := dag.
		Apko().
		Wolfi().
		WithPackages([]string{
			"gh",
			"git",
		}).
		Container().
		WithEnvVariable("GH_PROMPT_DISABLED", "true").
		WithEnvVariable("GH_NO_UPDATE_NOTIFIER", "true").
		WithEnvVariable("GH_HOST", host)

	if m.CACert != nil {
		ctr = ctr.
			WithMountedFile("/etc/ssl/certs/dagger-gh-ca.pem", m.CACert).
			WithEnvVariable("SSL_CERT_FILE", "/etc/ssl/certs/dagger-gh-ca.pem").
			WithEnvVariable("GIT_SSL_CAINFO", "/etc/ssl/certs/dagger-gh-ca.pem")
	}

	return ctr.WithExec([]string{"gh", "auth", "setup-git", "--force", "--hostname", host}) // Use force to avoid network call and cache setup even when no token is provided.
}

func (m *Gh) container(token *dagger.Secret, repo string) *dagger.Container {
	if token == nil {
		token = m.Token
	}

	if repo == "" {
		repo = m.Repository
	}

	host := m.host()
	return m.base().
		WithEnvVariable("CACHE_BUSTER", time.Now().Format(time.RFC3339Nano)).
		With(func(c *dagger.Container) *dagger.Container {
			if token != nil {
				if host == "github.com" {
					c = c.WithSecretVariable("GITHUB_TOKEN", token)
				} else {
					c = c.WithSecretVariable("GH_ENTERPRISE_TOKEN", token)
				}
			}

			if repo != "" {
				c = c.WithEnvVariable("GH_REPO", repo)
			}

			if m.Source != nil {
				c = c.
					WithWorkdir("/work/repo").
					WithMountedDirectory("/work/repo", m.Source)
			}

			return c
		})
}
