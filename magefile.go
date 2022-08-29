//go:build mage
// +build mage

package main

import (
	"context"
	"runtime"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

// Default target to run when none is specified
// If not set, running mage will list available targets
// var Default = Build

func Build(ctx context.Context) error {
	return engine.Start(ctx, nil, func(ctx engine.Context) error {
		wd, err := workdir(ctx)
		if err != nil {
			return err
		}

		buildResponse := struct {
			Core struct {
				Image struct {
					Exec struct {
						Mount struct {
							ID string
						}
					}
				}
			}
		}{}
		err = ctx.Client.MakeRequest(ctx.Context, &graphql.Request{
			Query: `
			query($source: FSID!, $os: String!, $arch: String!) {
				core {
					image(ref: "golang:1.19-alpine") {
						exec(input: {
							args: ["/usr/local/go/bin/go", "build", "-o", "bin/cloak", "./cmd/cloak"],
							mounts: [{fs: $source, path: "/src"}],
							env: [{name: "GOOS", value: $os}, {name: "GOARCH", value: $arch}],
							workdir: "/src",
						}) {
							mount(path: "/src") {
								id
							}
						}
					}
				}
			}
			`,
			Variables: map[string]interface{}{
				"source": wd,
				"os":     runtime.GOOS,
				"arch":   runtime.GOARCH,
			},
		}, &graphql.Response{Data: &buildResponse})
		if err != nil {
			return err
		}

		// FIXME: need to only write the binary, this will overwrite every file.
		return ctx.Client.MakeRequest(ctx.Context, &graphql.Request{
			Query: `
			query($build: FSID!) {
				host {
					workdir {
						write(contents: $build)
					}
				}
			}
			`,
			Variables: map[string]interface{}{
				"build": buildResponse.Core.Image.Exec.Mount.ID,
			},
		}, &graphql.Response{})
	})
}

func workdir(ctx engine.Context) (dagger.FSID, error) {
	resp := struct {
		Host struct {
			Workdir struct {
				Read struct {
					ID dagger.FSID
				}
			}
		}
	}{}
	err := ctx.Client.MakeRequest(ctx.Context, &graphql.Request{
		Query: `
		query {
			host {
				workdir {
					read {
						id
					}
				}
			}
		}
		`,
	}, &graphql.Response{Data: &resp})
	return resp.Host.Workdir.Read.ID, err
}
