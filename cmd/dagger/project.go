package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/projectconfig"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	projectURI   string
	projectFlags = pflag.NewFlagSet("project", pflag.ContinueOnError)

	sdk         string
	projectName string
	projectRoot string
)

const (
	projectURIDefault = "."
)

func init() {
	projectFlags.StringVarP(&projectURI, "project", "p", projectURIDefault, "Path to dagger.json config file for the project or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger?ref=branch?subpath=path/to/some/dir\").")
	projectCmd.PersistentFlags().AddFlagSet(projectFlags)
	doCmd.PersistentFlags().AddFlagSet(projectFlags)

	projectInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK to use for the project")
	projectInitCmd.MarkFlagRequired("sdk")
	projectInitCmd.PersistentFlags().StringVar(&projectName, "name", "", "Name of the new project")
	projectInitCmd.MarkFlagRequired("name")
	projectInitCmd.PersistentFlags().StringVarP(&projectRoot, "root", "", "", "Root directory that should be loaded for the full project context. Defaults to the parent directory containing dagger.json.")

	projectCmd.AddCommand(projectInitCmd)
}

var projectCmd = &cobra.Command{
	Use:    "project",
	Short:  "Manage dagger projects",
	Long:   "Manage dagger projects. By default, print the configuration of the specified project in json format.",
	Hidden: true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		proj, err := getProjectFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}
		var cfg *projectconfig.Config
		switch {
		case proj.local != nil:
			cfg, err = proj.local.config()
			if err != nil {
				return fmt.Errorf("failed to get local project config: %w", err)
			}
		case proj.git != nil:
			// we need to read the git repo, which currently requires an engine+client
			err = withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
				c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
				if err != nil {
					return fmt.Errorf("failed to connect to dagger: %w", err)
				}
				defer c.Close()
				cfg, err = proj.git.config(ctx, c)
				if err != nil {
					return fmt.Errorf("failed to get git project config: %w", err)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal project config: %w", err)
		}
		cmd.Println(string(cfgBytes))
		return nil
	},
}

var projectInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger project in a local directory.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		proj, err := getProjectFlagConfig()
		if err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}
		if proj.git != nil {
			return fmt.Errorf("project init is not supported for git projects")
		}

		if _, err := os.Stat(proj.local.path); err == nil {
			return fmt.Errorf("project init config path already exists: %s", proj.local.path)
		}
		switch projectconfig.SDK(sdk) {
		case projectconfig.SDKGo, projectconfig.SDKPython:
		default:
			return fmt.Errorf("unsupported project SDK: %s", sdk)
		}
		cfg := &projectconfig.Config{
			Name: projectName,
			SDK:  sdk,
			Root: projectRoot,
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal project config: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(proj.local.path), 0755); err != nil {
			return fmt.Errorf("failed to create project config directory: %w", err)
		}
		// nolint:gosec
		if err := os.WriteFile(proj.local.path, cfgBytes, 0644); err != nil {
			return fmt.Errorf("failed to write project config: %w", err)
		}
		return nil
	},
}

func getProjectFlagConfig() (*projectFlagConfig, error) {
	projectURI := projectURI
	if projectURI == "" || projectURI == projectURIDefault {
		// it's unset or default value, use env if present
		if v, ok := os.LookupEnv("DAGGER_PROJECT"); ok {
			projectURI = v
		}
	}

	url, err := url.Parse(projectURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "", "local": // local path
		projPath, err := filepath.Abs(url.Host + url.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get project absolute path: %w", err)
		}

		if filepath.Base(projPath) != "dagger.json" {
			projPath = filepath.Join(projPath, "dagger.json")
		}

		return &projectFlagConfig{local: &localProject{
			path: projPath,
		}}, nil
	case "git":
		repo := url.Host + url.Path

		// options for git projects are set via query params
		subpath := url.Query().Get("subpath")
		if path.Base(subpath) != "dagger.json" {
			subpath = path.Join(subpath, "dagger.json")
		}

		gitRef := url.Query().Get("ref")
		if gitRef == "" {
			gitRef = "main"
		}

		gitProtocol := url.Query().Get("protocol")
		if gitProtocol != "" {
			repo = gitProtocol + "://" + repo
		}

		p := &gitProject{
			subpath: subpath,
			repo:    repo,
			ref:     gitRef,
		}
		return &projectFlagConfig{git: p}, nil
	default:
		return nil, fmt.Errorf("unsupported project URI scheme: %s", url.Scheme)
	}
}

// projectFlagConfig holds the project settings provided by the user via flags (or defaults if not set)
type projectFlagConfig struct {
	// only one of these will be set
	local *localProject
	git   *gitProject
}

func (p projectFlagConfig) load(ctx context.Context, c *dagger.Client) (*dagger.Project, error) {
	switch {
	case p.local != nil:
		return p.local.load(c)
	case p.git != nil:
		return p.git.load(ctx, c)
	default:
		panic("invalid project")
	}
}

type localProject struct {
	path string
}

func (p localProject) config() (*projectconfig.Config, error) {
	configBytes, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read local config file: %w", err)
	}
	var cfg projectconfig.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse local config file: %w", err)
	}
	return &cfg, nil
}

func (p localProject) load(c *dagger.Client) (*dagger.Project, error) {
	rootDir, err := p.rootDir()
	if err != nil {
		return nil, err
	}
	subdirRelPath, err := filepath.Rel(rootDir, p.path)
	if err != nil {
		return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
	}
	if strings.HasPrefix(subdirRelPath, "..") {
		return nil, fmt.Errorf("project config path %q is not under project root %q", p.path, rootDir)
	}
	return c.Project().Load(c.Host().Directory(rootDir), subdirRelPath), nil
}

func (p localProject) rootDir() (string, error) {
	cfg, err := p.config()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(p.path), cfg.Root)), nil
}

type gitProject struct {
	subpath string
	repo    string
	ref     string
}

func (p gitProject) config(ctx context.Context, c *dagger.Client) (*projectconfig.Config, error) {
	configStr, err := c.Git(p.repo).Branch(p.ref).Tree().File(p.subpath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read git config file: %w", err)
	}
	var cfg projectconfig.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse git config file: %w", err)
	}
	return &cfg, nil
}

func (p gitProject) load(ctx context.Context, c *dagger.Client) (*dagger.Project, error) {
	cfg, err := p.config(ctx, c)
	if err != nil {
		return nil, err
	}
	rootPath := filepath.Clean(filepath.Join(filepath.Dir(p.subpath), cfg.Root))
	subdirRelPath, err := filepath.Rel(rootPath, p.subpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
	}
	if strings.HasPrefix(subdirRelPath, "..") {
		return nil, fmt.Errorf("project config path %q is not under project root %q", p.subpath, rootPath)
	}
	return c.Project().Load(c.Git(p.repo).Branch(p.ref).Tree().Directory(rootPath), subdirRelPath), nil
}
