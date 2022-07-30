package main

// THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/cloak/examples/netlify/go/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"

	openAPIClient "github.com/go-openapi/runtime/client"
	netlifyModel "github.com/netlify/open-api/v2/go/models"
	netlifyOps "github.com/netlify/open-api/v2/go/plumbing/operations"
	netlify "github.com/netlify/open-api/v2/go/porcelain"
	netlifycontext "github.com/netlify/open-api/v2/go/porcelain/context"
)

type Resolver struct{}

func (r *queryResolver) Deploy(ctx context.Context, contents dagger.FSID, subdir *string, siteName *string, token dagger.SecretID) (*Deploy, error) {
	// Setup Auth
	readSecretOutput, err := core.Secret(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}
	ctx = netlifycontext.WithAuthInfo(ctx, openAPIClient.BearerToken(readSecretOutput.Core.Secret))

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
	// print the contents of deployDir
	dirents, err := os.ReadDir("/mnt")
	if err != nil {
		return nil, fmt.Errorf("failed to read deployDir: %w", err)
	}
	for _, dirent := range dirents {
		fmt.Println(dirent.Name())
	}

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

	return &Deploy{
		URL:       deploy.URL,
		DeployURL: deploy.DeployURL,
	}, nil
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() *queryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){
		"Deploy": func(rctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var err error
			fc.Args, err = (&executionContext{}).field_Query_deploy_args(rctx, fc.Args)
			if err != nil {
				return nil, err
			}
			obj, ok := fc.ParentResult.(struct{})
			_ = ok
			_ = obj
			qr := &queryResolver{}
			return qr.Query().Deploy(rctx, fc.Args["contents"].(dagger.FSID), fc.Args["subdir"].(*string), fc.Args["siteName"].(*string), fc.Args["token"].(dagger.SecretID))
		},
	})
}
