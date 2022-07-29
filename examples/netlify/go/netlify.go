package main

// THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/cloak/examples/netlify/go/gen/core"
	"github.com/dagger/cloak/examples/netlify/go/gen/netlify/generated"
	"github.com/dagger/cloak/examples/netlify/go/gen/netlify/model"
	"github.com/dagger/cloak/sdk/go/dagger"

	openAPIClient "github.com/go-openapi/runtime/client"
	netlifyModel "github.com/netlify/open-api/v2/go/models"
	netlifyOps "github.com/netlify/open-api/v2/go/plumbing/operations"
	netlify "github.com/netlify/open-api/v2/go/porcelain"
	netlifycontext "github.com/netlify/open-api/v2/go/porcelain/context"
)

type Resolver struct{}

func (r *queryResolver) Deploy(ctx context.Context, contents dagger.FS, subdir *string, siteName *string, token dagger.Secret) (*model.Deploy, error) {
	// Setup Auth
	readSecretOutput, err := core.ReadSecret(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}
	ctx = netlifycontext.WithAuthInfo(ctx, openAPIClient.BearerToken(readSecretOutput.Readsecret))

	// Get the site metadata
	var site *netlifyModel.Site
	sites, err := netlify.Default.ListSites(ctx, &netlifyOps.ListSitesParams{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("failed to list sites: %w", err)
	}
	for _, s := range sites {
		if s.Name == *siteName {
			site = s
			break
		}
	}

	// If the site doesn't exist already, create it
	if site == nil {
		site, err = netlify.Default.CreateSite(ctx, &netlifyModel.SiteSetup{Site: netlifyModel.Site{
			Name: *siteName,
		}}, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create site: %w", err)
		}
	}

	// Deploy the site contents
	deployDir := "/mnt/contents" // TODO: add sugar to dagger.FS for this
	if subdir != nil {
		deployDir = filepath.Join(deployDir, *subdir)
	}
	deploy, err := netlify.Default.DeploySite(ctx, netlify.DeployOptions{
		SiteID: site.ID,
		Dir:    deployDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to deploy site: %w", err)
	}
	_, err = netlify.Default.WaitUntilDeployLive(ctx, deploy)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deploy: %w", err)
	}

	return &model.Deploy{
		URL:       deploy.URL,
		DeployURL: deploy.DeployURL,
	}, nil
}

// Query returns generated.QueryResolver implementation.
func (r *Resolver) Query() generated.QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
