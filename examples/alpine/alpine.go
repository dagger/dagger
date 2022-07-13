package main

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/cloak/dagger"
)

func Build(ctx *dagger.Context, input *dagger.AlpineBuildInput) *dagger.AlpineBuild {
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
	var result dagger.CoreResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		panic(err)
	}
	fs := result.Core.Image.FS

	// install each of the requested packages
	for _, pkg := range input.Pkgs {
		fsBytes, err := json.Marshal(fs)
		if err != nil {
			panic(err)
		}
		output, err := dagger.Do(ctx, fmt.Sprintf(`{core{exec(fs:%q,args:["apk", "add", "-U", "--no-cache", %q]){fs}}}`, string(fsBytes), pkg))
		if err != nil {
			panic(err)
		}
		var result dagger.CoreResult
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			panic(err)
		}
		fs = result.Core.Exec.FS
	}

	return &dagger.AlpineBuild{FS: fs}
}
