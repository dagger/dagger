package config

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/sdk/go/dagger"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Path       string                `yaml:"-,omitempty"`
	Extensions map[string]*Extension `yaml:"extensions,omitempty"`
	Context    string                `yaml:"context,omitempty"`
}

type Extension struct {
	Local      string `yaml:"local,omitempty"`
	Image      string `yaml:"image,omitempty"`
	Context    string `yaml:"context,omitempty"`
	schema     string
	operations string
}

func (a *Extension) GetSchema() string {
	return a.schema
}

func (a *Extension) GetOperations() string {
	return a.operations
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
	if cfg.Extensions == nil {
		cfg.Extensions = make(map[string]*Extension)
	}

	for _, ext := range cfg.Extensions {
		if ext.Local != "" {
			if ext.Context == "" {
				ext.Context = cfg.Context
			}
			ext.Context = filepath.Join(filepath.Dir(f), ext.Context)
			ext.Local = filepath.Join(cfg.Context, ext.Local)
		}
	}
	// implicitly include core in every import
	cfg.Extensions["core"] = &Extension{}

	loaded, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "Loading:\n%s\n", string(loaded))

	return &cfg, nil
}

func (c *Config) LocalDirs() map[string]string {
	localDirs := make(map[string]string)
	for _, ext := range c.Extensions {
		if ext.Local == "" {
			continue
		}
		localDirs[ext.Context] = ext.Context
	}
	return localDirs
}

func (c *Config) LoadExtensions(ctx context.Context, localDirs map[string]dagger.FSID) error {
	var eg errgroup.Group
	for name, ext := range c.Extensions {
		name := name
		ext := ext
		eg.Go(func() error {
			switch {
			case name == "core":
				schema, operations, err := importCore(ctx)
				if err != nil {
					return fmt.Errorf("error importing %s: %w", name, err)
				}
				ext.schema = schema
				ext.operations = operations
			case ext.Local != "":
				dockerfile := path.Join(ext.Local, "Dockerfile")
				schema, operations, err := importLocal(ctx, name, localDirs[ext.Context], dockerfile)
				if err != nil {
					return fmt.Errorf("error importing %s: %w", name, err)
				}
				ext.schema = schema
				ext.operations = operations
			}
			return nil
		})
	}

	return eg.Wait()
}

func importLocal(ctx context.Context, name string, cwd dagger.FSID, dockerfile string) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}
	data := struct {
		Core struct {
			Filesystem struct {
				Dockerbuild struct {
					ID dagger.FSID
				}
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Dockerfile($context: FSID!, $dockerfile: String!) {
				core {
					filesystem(id: $context) {
						dockerbuild(dockerfile: $dockerfile) {
							id
						}
					}
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
		return "", "", err
	}
	return importFS(ctx, name, data.Core.Filesystem.Dockerbuild.ID)
}

func importFS(ctx context.Context, name string, fs dagger.FSID) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}

	data := struct {
		Core struct {
			Filesystem struct {
				LoadExtension struct {
					Schema     string
					Operations string
				}
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query LoadExtension($fs: FSID!, $name: String!) {
				core {
					filesystem(id: $fs) {
						loadExtension(name: $name) {
							schema
							operations
						}
					}
				}
			}`,
			Variables: map[string]any{
				"fs":   fs,
				"name": name,
			},
		},
		resp,
	)
	if err != nil {
		return "", "", err
	}
	return data.Core.Filesystem.LoadExtension.Schema, data.Core.Filesystem.LoadExtension.Operations, nil
}

func importCore(ctx context.Context) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}

	data := struct {
		Core struct {
			Extension struct {
				Schema     string
				Operations string
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query {
				core {
					extension(name: "core") {
						schema
						operations
					}
				}
			}`,
		},
		resp,
	)
	if err != nil {
		return "", "", err
	}
	return data.Core.Extension.Schema, data.Core.Extension.Operations, nil
}
