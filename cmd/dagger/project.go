package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	projectURI   string
	configPath   string
	projectFlags = pflag.NewFlagSet("project", pflag.ContinueOnError)

	sdk         string
	projectName string
)

const (
	projectURIDefault = "."
)

func init() {
	projectFlags.StringVarP(&projectURI, "project", "p", projectURIDefault, "Location of the project root, either local path (e.g. \"/path/to/some/dir\") or a git repo (e.g. \"git://github.com/dagger/dagger#branchname\").")
	projectFlags.StringVarP(&configPath, "config", "c", "./dagger.json", "Path to dagger.json config file for the project, or a parent directory containing that file, relative to the project's root directory.")
	projectCmd.PersistentFlags().AddFlagSet(projectFlags)
	doCmd.PersistentFlags().AddFlagSet(projectFlags)

	projectInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK to use for the project")
	projectInitCmd.MarkFlagRequired("sdk")
	projectInitCmd.PersistentFlags().StringVar(&projectName, "name", "", "Name of the new project")
	projectInitCmd.MarkFlagRequired("name")

	projectCmd.AddCommand(projectInitCmd)
}

var projectCmd = &cobra.Command{
	Use:    "project",
	Hidden: true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		proj, err := getProjectConfig()
		if err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}
		var cfg *core.ProjectConfig
		switch {
		case proj.local != nil:
			cfg, err = proj.local.config()
			if err != nil {
				return fmt.Errorf("failed to get local project config: %w", err)
			}
		case proj.git != nil:
			err = withEngineAndTUI(ctx, engine.Config{}, func(ctx context.Context, r *router.Router) (err error) {
				opts := []dagger.ClientOpt{
					dagger.WithConn(router.EngineConn(r)),
				}
				c, err := dagger.Connect(ctx, opts...)
				if err != nil {
					return fmt.Errorf("failed to connect to dagger: %w", err)
				}
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
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		proj, err := getProjectConfig()
		if err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}
		if proj.git != nil {
			return fmt.Errorf("project init is not supported for git projects")
		}
		fullConfigPath := filepath.Join(proj.local.rootPath, proj.local.configPath)
		if _, err := os.Stat(fullConfigPath); err == nil {
			return fmt.Errorf("project init config path already exists: %s", fullConfigPath)
		}
		switch core.ProjectSDK(sdk) {
		case core.ProjectSDKGo, core.ProjectSDKPython:
		default:
			return fmt.Errorf("unsupported project SDK: %s", sdk)
		}
		cfg := &core.ProjectConfig{
			Name: projectName,
			SDK:  sdk,
		}
		cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal project config: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(fullConfigPath), 0755); err != nil {
			return fmt.Errorf("failed to create project config directory: %w", err)
		}
		// nolint:gosec
		if err := os.WriteFile(fullConfigPath, cfgBytes, 0644); err != nil {
			return fmt.Errorf("failed to write project config: %w", err)
		}
		return nil
	},
}

func getProjectConfig() (*project, error) {
	projectURI, configPath := projectURI, configPath
	if projectURI == "" || projectURI == projectURIDefault {
		// it's unset or default value, use env if present
		if v, ok := os.LookupEnv("DAGGER_PROJECT"); ok {
			projectURI = v
		}
	}

	if filepath.Base(configPath) != "dagger.json" {
		configPath = filepath.Join(configPath, "dagger.json")
	}

	url, err := url.Parse(projectURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "", "local": // local path
		projectAbsPath, err := filepath.Abs(projectURI)
		if err != nil {
			return nil, fmt.Errorf("failed to get project absolute path: %w", err)
		}
		configAbsPath, err := filepath.Abs(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get config absolute path: %w", err)
		}
		configRelPath, err := filepath.Rel(projectAbsPath, configAbsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get config relative path: %w", err)
		}
		return &project{local: &localProject{
			configPath: configRelPath,
			rootPath:   projectAbsPath,
		}}, nil
	case "git":
		repo := url.Host + url.Path
		// TODO:(sipsma) just change ref to be a query param too?
		ref := url.Fragment
		if ref == "" {
			ref = "main"
		}
		gitProtocol := url.Query().Get("protocol")
		if gitProtocol != "" {
			repo = gitProtocol + "://" + repo
		}
		p := &gitProject{
			configPath: configPath,
			repo:       repo,
			ref:        ref,
		}
		return &project{git: p}, nil
	default:
		return nil, fmt.Errorf("unsupported project URI scheme: %s", url.Scheme)
	}
}

type project struct {
	// only one of these will be set
	local *localProject
	git   *gitProject
}

func (p project) load(c *dagger.Client) *dagger.Project {
	switch {
	case p.local != nil:
		return p.local.load(c)
	case p.git != nil:
		return p.git.load(c)
	default:
		panic("invalid project")
	}
}

type localProject struct {
	configPath string
	rootPath   string
}

func (p localProject) config() (*core.ProjectConfig, error) {
	configBytes, err := os.ReadFile(filepath.Join(p.rootPath, p.configPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read local config file: %w", err)
	}
	var cfg core.ProjectConfig
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse local config file: %w", err)
	}
	return &cfg, nil
}

func (p localProject) load(c *dagger.Client) *dagger.Project {
	return c.Project().Load(c.Host().Directory(p.rootPath), p.configPath)
}

type gitProject struct {
	configPath string
	repo       string
	ref        string
}

func (p gitProject) load(c *dagger.Client) *dagger.Project {
	return c.Project().Load(p.dir(c), p.configPath)
}

func (p gitProject) config(ctx context.Context, c *dagger.Client) (*core.ProjectConfig, error) {
	configStr, err := p.dir(c).File(p.configPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read git config file: %w", err)
	}
	var cfg core.ProjectConfig
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse git config file: %w", err)
	}
	return &cfg, nil
}

func (p gitProject) dir(c *dagger.Client) *dagger.Directory {
	return c.Git(p.repo).Branch(p.ref).Tree()
}
