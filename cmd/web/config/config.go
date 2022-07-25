package config

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/sdk/go/dagger"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Path    string             `yaml:"-,omitempty"`
	Actions map[string]*Action `yaml:"actions,omitempty"`
}

type Action struct {
	Local      string `yaml:"local,omitempty"`
	Image      string `yaml:"image,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

func ParseFile(f string) (*Config, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}

	cfg := Config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	for _, action := range cfg.Actions {
		if action.Local != "" {
			action.Local = path.Join(filepath.Dir(f), action.Local)
		}
	}

	loaded, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "Loading:\n%s\n", string(loaded))

	return &cfg, nil
}

func (c *Config) LocalDirs() map[string]string {
	localDirs := make(map[string]string)
	for _, action := range c.Actions {
		if action.Local != "" {
			localDirs[action.Local] = action.Local
		}
	}
	return localDirs
}

func (c *Config) Import(ctx context.Context, localDirs map[string]dagger.FS) error {
	for name, action := range c.Actions {
		switch {
		case action.Local != "":
			err := importLocal(ctx, name, localDirs[action.Local], action.Dockerfile)
			if err != nil {
				return fmt.Errorf("error importing %s: %w", name, err)
			}
		case action.Image != "":
			err := importImage(ctx, name, action.Image)
			if err != nil {
				return fmt.Errorf("error importing %s: %w", name, err)
			}
		}
	}

	return nil
}

func importLocal(ctx context.Context, name string, cwd dagger.FS, dockerfile string) error {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return err
	}
	data := struct {
		Core struct {
			Dockerfile dagger.FS
		}
	}{}
	resp := &graphql.Response{Data: &data}
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Dockerfile($context: FS!, $dockerfile: String!) {
				core {
					dockerfile(
						context: $context,
						dockerfileName: $dockerfile,
					)
				}
			}`,
			Variables: map[string]any{
				"context":    cwd,
				"dockerfile": dockerfile,
			},
		},
		resp,
	)
	if err != nil {
		return err
	}
	return importFS(ctx, name, data.Core.Dockerfile)
}

func importImage(ctx context.Context, name string, ref string) error {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return err
	}
	data := struct {
		Core struct {
			Image struct {
				FS dagger.FS
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Image($ref: String!) {
				core {
					image(ref: $ref) {
						fs
					}
				}
			}`,
			Variables: map[string]any{
				"ref": ref,
			},
		},
		resp,
	)
	if err != nil {
		return err
	}
	return importFS(ctx, name, data.Core.Image.FS)
}

func importFS(ctx context.Context, name string, fs dagger.FS) error {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return err
	}

	return cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			mutation Import($name: String!, $fs: FS!) {
				import(name: $name, fs: $fs) {
						name
				}
			}`,
			Variables: map[string]any{
				"name": name,
				"fs":   fs,
			},
		},
		&graphql.Response{},
	)
}
