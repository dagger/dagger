package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/project"
	"github.com/spf13/cobra"
)

/* TODO:
* replace command?
* upgrade command (makes sense once locking exists)
 */

var (
	initName string
	initSDK  string

	addLocalPath string

	addGitRemote  string
	addGitRef     string
	addGitSubpath string

	rmName string
)

var projectCmd = &cobra.Command{
	Use: "project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getThisProjectConfig()
		if err != nil {
			return err
		}
		return printConfig(cfg)
	},
}

var initCmd = &cobra.Command{
	Use: "init",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := &project.Config{
			Name: initName,
			SDK:  initSDK,
		}
		return writeConfigFile(cfg)
	},
}

var addCmd = &cobra.Command{
	Use: "add",
}

var addLocalCmd = &cobra.Command{
	Use: "local",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getThisProjectConfig()
		if err != nil {
			return err
		}
		if cfg.Extensions == nil {
			cfg.Extensions = make(map[string]project.Extension)
		}
		otherCfg, err := getConfig(addLocalPath)
		if err != nil {
			return err
		}
		cfg.Extensions[otherCfg.Name] = project.Extension{
			Local: &project.LocalExtension{
				Path: addLocalPath,
			},
		}
		return writeConfigFile(cfg)
	},
}

var addGitCmd = &cobra.Command{
	Use: "git",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg, err := getThisProjectConfig()
		if err != nil {
			return err
		}

		cl, err := dagger.Connect(ctx,
			dagger.WithWorkdir(workdir),
			dagger.WithConfigPath(configPath),
			dagger.WithNoExtensions(),
		)
		if err != nil {
			return err
		}
		defer cl.Close()

		// TODO:(sipsma) this shouldn't need to start with an actual config, should just need core API
		proj, err := loadGitProject(ctx, cl, project.GitExtension{
			Remote: addGitRemote,
			Ref:    addGitRef,
			Path:   addGitSubpath,
		})
		if err != nil {
			return err
		}
		cfg.Extensions[proj.Name] = project.Extension{
			Git: &project.GitExtension{
				Remote: addGitRemote,
				Ref:    addGitRef,
				Path:   addGitSubpath,
			},
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		return writeConfigFile(cfg)
	},
}

var rmCmd = &cobra.Command{
	Use: "rm",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getThisProjectConfig()
		if err != nil {
			return err
		}
		delete(cfg.Extensions, rmName)
		return writeConfigFile(cfg)
	},
}

func getConfig(p string) (*project.Config, error) {
	bytes, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var config project.Config
	if err := json.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func getThisProjectConfig() (*project.Config, error) {
	return getConfig(configPath)
}

func writeConfig(dest io.Writer, cfg *project.Config) error {
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	_, err = dest.Write(append(bytes, '\n'))
	return err
}

func printConfig(cfg *project.Config) error {
	return writeConfig(os.Stdout, cfg)
}

func writeConfigFile(cfg *project.Config) error {
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	return writeConfig(f, cfg)
}

func loadGitProject(ctx context.Context, cl *dagger.Client, gitParams project.GitExtension) (*schema.Project, error) {
	res := struct {
		Git struct {
			Branch struct {
				Tree struct {
					LoadProject schema.Project
				}
			}
		}
	}{}
	resp := &dagger.Response{Data: &res}

	// TODO: update to new API once loadProject is migrated
	err := cl.Do(ctx,
		&dagger.Request{
			Query: `
			query Load($remote: String!, $ref: String!, $subpath: String!) {
				git(url: $remote) {
					branch(name: $ref) {
						tree {
							loadProject(configPath: $subpath) {
								name
							}
						}
					}
				}
			}`,
			Variables: map[string]any{
				"remote":  gitParams.Remote,
				"ref":     gitParams.Ref,
				"subpath": gitParams.Path,
			},
		},
		resp,
	)
	if err != nil {
		return nil, err
	}

	return &res.Git.Branch.Tree.LoadProject, nil
}
