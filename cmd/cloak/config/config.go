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
	Path    string             `yaml:"-,omitempty"`
	Actions map[string]*Action `yaml:"actions,omitempty"`
	Context string             `yaml:"context,omitempty"`
}

type Action struct {
	Local      string `yaml:"local,omitempty"`
	Image      string `yaml:"image,omitempty"`
	Context    string `yaml:"context,omitempty"`
	schema     string
	operations string
}

func (a *Action) GetSchema() string {
	return a.schema
}

func (a *Action) GetOperations() string {
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
	if cfg.Actions == nil {
		cfg.Actions = make(map[string]*Action)
	}

	for _, action := range cfg.Actions {
		if action.Local != "" {
			if action.Context == "" {
				action.Context = cfg.Context
			}
			action.Context = filepath.Join(filepath.Dir(f), action.Context)
			action.Local = filepath.Join(cfg.Context, action.Local)
		}
	}
	// implicitly include core in every import
	cfg.Actions["core"] = &Action{}

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
		if action.Local == "" {
			continue
		}
		localDirs[action.Context] = action.Context
	}
	return localDirs
}

func (c *Config) Import(ctx context.Context, localDirs map[string]dagger.FSID) error {
	var eg errgroup.Group
	for name, action := range c.Actions {
		name := name
		action := action
		eg.Go(func() error {
			switch {
			case name == "core":
				schema, operations, err := importCore(ctx)
				if err != nil {
					return fmt.Errorf("error importing %s: %w", name, err)
				}
				action.schema = schema
				action.operations = operations
			case action.Image != "":
				schema, operations, err := importImage(ctx, name, action.Image)
				if err != nil {
					return fmt.Errorf("error importing %s: %w", name, err)
				}
				action.schema = schema
				action.operations = operations
			case action.Local != "":
				dockerfile := path.Join(action.Local, "Dockerfile")
				schema, operations, err := importLocal(ctx, name, localDirs[action.Context], dockerfile)
				if err != nil {
					return fmt.Errorf("error importing %s: %w", name, err)
				}
				action.schema = schema
				action.operations = operations
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
					Id dagger.FSID
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
	return importFS(ctx, name, data.Core.Filesystem.Dockerbuild.Id)
}

func importImage(ctx context.Context, name string, ref string) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}
	data := struct {
		Core struct {
			Image struct {
				Id dagger.FSID
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
						id
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
		return "", "", err
	}
	return importFS(ctx, name, data.Core.Image.Id)
}

func importFS(ctx context.Context, name string, fs dagger.FSID) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}

	data := struct {
		Import struct {
			Schema     string
			Operations string
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			mutation Import($name: String!, $fs: FSID!) {
				import(name: $name, fs: $fs) {
						schema
						operations
				}
			}`,
			Variables: map[string]any{
				"name": name,
				"fs":   fs,
			},
		},
		resp,
	)
	if err != nil {
		return "", "", err
	}
	return data.Import.Schema, data.Import.Operations, nil
}

// technically, core doesn't need to be imported, but this allows us to get its schema+operations
func importCore(ctx context.Context) (schema, operations string, err error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return "", "", err
	}

	data := struct {
		Import struct {
			Schema     string
			Operations string
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			mutation Import {
				import(name: "core") {
						schema
						operations
				}
			}`,
		},
		resp,
	)
	if err != nil {
		return "", "", err
	}
	return data.Import.Schema, data.Import.Operations, nil
}
