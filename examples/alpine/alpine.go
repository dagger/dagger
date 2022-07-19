package main

import (
	"context"
	"encoding/json"
	"fmt"

	dagger "github.com/dagger/cloak/sdk/go"
)

func Build(ctx context.Context, input map[string]interface{}) interface{} {
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
	result := make(map[string]interface{})
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		panic(err)
	}
	fs := result["core"].(map[string]interface{})["image"].(map[string]interface{})["fs"]

	// install each of the requested packages
	for _, pkg := range input["pkgs"].([]interface{}) {
		pkg := pkg.(string)
		output, err := dagger.Do(ctx, fmt.Sprintf(`{core{exec(fs:%q,args:["apk", "add", "-U", "--no-cache", %q]){fs}}}`, fs.(string), pkg))
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			panic(err)
		}
		fs = result["core"].(map[string]interface{})["exec"].(map[string]interface{})["fs"]
	}

	return fs
}
