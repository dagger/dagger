package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func (r *filesystem) debug(ctx context.Context, parent *dagger.Filesystem) (*Debug, error) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		return nil, err
	}

	resp := struct {
		Core struct {
			Image struct {
				Start struct {
					ID string
				}
			}
		}
	}{}
	err = cl.MakeRequest(ctx, &graphql.Request{
		Query: `
		query debug($fs: FSID!) {
			core {
				image(ref: "alpine") {
					start(input: {
						args: ["sh"]
						mounts: [
							{
								path: "/mnt"
								fs: $fs
							}
						]
						workdir: "/mnt"
						env: [
							{
								name: "PS1"
								value: "\\033[38;5;229mcloak> \\033[0m"
							}
						]
					}) {
						id
					}
				}
			}
		}`,
		Variables: map[string]interface{}{
			"fs": parent.ID,
		}},
		&graphql.Response{
			Data: &resp,
		},
	)
	if err != nil {
		return nil, err
	}

	return &Debug{
		Fs:      parent,
		Session: resp.Core.Image.Start.ID,
	}, nil
}
