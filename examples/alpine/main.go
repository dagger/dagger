package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func (r *alpine) build(ctx context.Context, pkgs []string) (*dagger.Filesystem, error) {
	client, err := dagger.Client(ctx)
	if err != nil {
		return nil, err
	}

	// start with Alpine base
	fsid, err := image(ctx, client, "alpine:3.15")
	if err != nil {
		return nil, err
	}

	// install each of the requested packages
	for _, pkg := range pkgs {
		fsid, err = addPkg(ctx, client, fsid, pkg)
		if err != nil {
			return nil, err
		}
	}
	return &dagger.Filesystem{ID: fsid}, nil
}

func image(ctx context.Context, client graphql.Client, ref string) (dagger.FSID, error) {
	req := &graphql.Request{
		Query: `
query Image ($ref: String!) {
	core {
		image(ref: $ref) {
			id
		}
	}
}
`,
		Variables: map[string]any{
			"ref": ref,
		},
	}
	resp := struct {
		Core struct {
			Image struct {
				ID dagger.FSID
			}
		}
	}{}
	err := client.MakeRequest(ctx, req, &graphql.Response{Data: &resp})
	if err != nil {
		return "", err
	}

	return resp.Core.Image.ID, nil
}

func addPkg(ctx context.Context, client graphql.Client, root dagger.FSID, pkg string) (dagger.FSID, error) {
	req := &graphql.Request{
		Query: `
query AddPkg ($root: FSID!, $pkg: String!) {
	core {
		filesystem(id: $root) {
			exec(input: {
				args: ["apk", "add", "-U", "--no-cache", $pkg]
			}) {
				fs {
					id
				}
			}
		}
	}
}
`,
		Variables: map[string]any{
			"root": root,
			"pkg":  pkg,
		},
	}
	resp := struct {
		Core struct {
			Filesystem struct {
				Exec struct {
					FS struct {
						ID dagger.FSID
					}
				}
			}
		}
	}{}
	err := client.MakeRequest(ctx, req, &graphql.Response{Data: &resp})
	if err != nil {
		return "", err
	}

	return resp.Core.Filesystem.Exec.FS.ID, nil
}
