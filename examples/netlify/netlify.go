//go:generate go run ../../stub -m ./sdk/netlify/model.gen.go -f ./main.gen.go
package main

import (
	"context"
	"fmt"

	"github.com/dagger/cloak/dagger"
	"github.com/dagger/cloak/dagger/core"
	"github.com/dagger/cloak/examples/alpine/sdk/alpine"
	"github.com/dagger/cloak/examples/netlify/sdk/netlify"

	"github.com/netlify/open-api/v2/go/models"
	"github.com/netlify/open-api/v2/go/plumbing/operations"
	"github.com/netlify/open-api/v2/go/porcelain"
)

func Deploy(ctx *dagger.Context, input *netlify.DeployInput) *netlify.DeployOutput {
	// get the site metadata
	var site *models.Site
	sites, err := porcelain.Default.ListSites(context.Background(), &operations.ListSitesParams{})
	if err != nil {
		panic(err)
	}
	for _, s := range sites {
		if s.Name == input.Site {
			site = s
			break
		}
	}
	if site == nil {
		// TODO: create the site rather than erroring
		panic(fmt.Errorf("site not found: %s", input.Site))
	}

	// base image for running netlify commands
	base := core.Exec(&core.ExecInput{
		// NOTE: what if one of these strings was from an action input that was lazy? Need string wrapper
		Base: alpine.Build(ctx, &alpine.BuildInput{Packages: []string{"curl", "jq", "npm"}}).FS,
		Dir:  "/",
		Args: []string{"npm", "-g", "install", "netlify-cli@8.6.21"},
	})

	// link the site
	base = core.Exec(&core.ExecInput{
		Base: base.FS,
		Dir:  "/src",
		Args: []string{"netlify", "link", "--id", site.ID},
		Mounts: []core.Mount{{
			FS:   input.Contents,
			Path: "/src",
		}},
		// NOTE: do we need "always" here?
	})

	// deploy the site
	base = core.Exec(&core.ExecInput{
		Base: base.FS,
		Dir:  "/src",
		Args: []string{"netlify", "deploy", "--build", "--site=" + site.ID, "--prod"},
		Mounts: []core.Mount{{
			FS:   input.Contents,
			Path: "/src",
		}},
		// NOTE: do we need "always" here?
	})

	// deploy synchronously
	if err := base.FS.Evaluate(ctx); err != nil {
		panic(err)
	}

	site, err = porcelain.Default.GetSite(context.Background(), site.ID)
	if err != nil {
		panic(err)
	}
	return &netlify.DeployOutput{
		URL:       site.URL,
		DeployURL: site.DeployURL,
	}
}
