package main

import (
	"context"
	"fmt"

	"github.com/dagger/cloak/sdk/go/dagger"
)

func Build(ctx context.Context, input dagger.Map) interface{} {
	/* TODO: update to use nice wrappers again
	output.Root = core.Image(ctx, &core.ImageInput{
		Ref: dagger.ToString("alpine:3.15.0"),
	}).FS()
	for _, pkg := range input.Packages {
		output.Root = core.Exec(ctx, &core.ExecInput{
			FS:   output.Root,
			Dir:  dagger.ToString("/"),
			Args: dagger.ToStrings("apk", "add", "-U", "--no-cache").Add(pkg),
		}).FS()
	}
	*/

	// start with Alpine base
	output, err := dagger.Do(ctx, `{core{image(ref:"alpine:3.15"){fs}}}`)
	if err != nil {
		panic(err)
	}
	fs := output.Map("core").Map("image").FS("fs")

	// install each of the requested packages
	for _, pkg := range input.StringList("pkgs") {
		output, err := dagger.Do(ctx, fmt.Sprintf(`{
			core {
				exec(input: {
					mounts:[{path:"/",fs:%s}],
					args:["apk", "add", "-U", "--no-cache", %s]
				}) {
					mount(path:"/")
				}
			}
		}`, fs, pkg))
		if err != nil {
			panic(err)
		}
		fs = output.Map("core").Map("exec").FS("mount")
	}

	return fs
}
