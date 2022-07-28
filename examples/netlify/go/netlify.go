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
	"github.com/netlify/open-api/v2/go/models"
	"github.com/netlify/open-api/v2/go/plumbing/operations"
	"github.com/netlify/open-api/v2/go/porcelain"
	netlifycontext "github.com/netlify/open-api/v2/go/porcelain/context"
)

type Resolver struct{}

func (r *queryResolver) Deploy(ctx context.Context, contents dagger.FS, subdir *string, siteName *string, token dagger.Secret) (*model.Deploy, error) {
	readSecretOutput, err := core.ReadSecret(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}
	ctx = netlifycontext.WithAuthInfo(ctx, openAPIClient.BearerToken(readSecretOutput.Readsecret))

	// get the site metadata
	var site *models.Site
	sites, err := porcelain.Default.ListSites(ctx, &operations.ListSitesParams{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("failed to list sites: %w", err)
	}
	for _, s := range sites {
		if s.Name == *siteName {
			site = s
			break
		}
	}
	if site == nil {
		site, err = porcelain.Default.CreateSite(ctx, &models.SiteSetup{Site: models.Site{
			Name: *siteName,
		}}, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create site: %w", err)
		}
	}

	deployDir := "/mnt/contents" // TODO: add sugar to dagger.FS for this
	if subdir != nil {
		deployDir = filepath.Join(deployDir, *subdir)
	}
	deploy, err := porcelain.Default.DeploySite(ctx, porcelain.DeployOptions{
		SiteID: site.ID,
		Dir:    deployDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to deploy site: %w", err)
	}
	_, err = porcelain.Default.WaitUntilDeployLive(ctx, deploy)
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
