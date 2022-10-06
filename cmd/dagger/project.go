package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.dagger.io/dagger/project"
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
)

var projectCmd = &cobra.Command{
	Use: "project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getConfig()
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

// TODO: hard, how do you identify it?
var rmCmd = &cobra.Command{
	Use: "rm",
}

var addCmd = &cobra.Command{
	Use: "add",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getConfig()
		if err != nil {
			return err
		}
		cfg.Extensions = append(cfg.Extensions, &project.Extension{
			Local: addLocalPath,
		})
		return writeConfigFile(cfg)
	},
}

var addLocalCmd = &cobra.Command{
	Use: "local",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getConfig()
		if err != nil {
			return err
		}
		cfg.Extensions = append(cfg.Extensions, &project.Extension{
			Local: addLocalPath,
		})
		return writeConfigFile(cfg)
	},
}

var addGitCmd = &cobra.Command{
	Use: "git",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := getConfig()
		if err != nil {
			return err
		}
		cfg.Extensions = append(cfg.Extensions, &project.Extension{
			Git: &project.GitSource{
				Remote: addGitRemote,
				Ref:    addGitRef,
				Path:   addGitSubpath,
			},
		})
		return writeConfigFile(cfg)
	},
}

func getConfig() (*project.Config, error) {
	p := filepath.Join(workdir, configPath)
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
	p := filepath.Join(workdir, configPath)
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	return writeConfig(f, cfg)
}
