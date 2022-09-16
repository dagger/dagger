package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/sdk/go/dagger"

	openAPIClient "github.com/go-openapi/runtime/client"
	netlifyModel "github.com/netlify/open-api/v2/go/models"
	netlifyOps "github.com/netlify/open-api/v2/go/plumbing/operations"
	netlifyClient "github.com/netlify/open-api/v2/go/porcelain"
	netlifycontext "github.com/netlify/open-api/v2/go/porcelain/context"
)

func (r *netlify) deploy(ctx context.Context, contents dagger.FSID, subdir *string, siteName *string, token dagger.SecretID) (*SiteURLs, error) {
	client, err := dagger.Client(ctx)
	if err != nil {
		return nil, err
	}

	// Setup Auth
	tokenPlaintext, err := secret(ctx, client, token)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}
	ctx = netlifycontext.WithAuthInfo(ctx, openAPIClient.BearerToken(tokenPlaintext))

	// Get the site metadata
	var site *netlifyModel.Site
	sites, err := netlifyClient.Default.ListSites(ctx, &netlifyOps.ListSitesParams{Context: ctx})
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
		site, err = netlifyClient.Default.CreateSite(ctx, &netlifyModel.SiteSetup{Site: netlifyModel.Site{
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
	deploy, err := netlifyClient.Default.DeploySite(ctx, netlifyClient.DeployOptions{
		SiteID: site.ID,
		Dir:    deployDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to deploy site: %w", err)
	}
	_, err = netlifyClient.Default.WaitUntilDeployLive(ctx, deploy)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for deploy: %w", err)
	}

	return &SiteURLs{
		URL:       deploy.URL,
		DeployURL: deploy.DeployURL,
	}, nil
}

func secret(ctx context.Context, client graphql.Client, id dagger.SecretID) (string, error) {
	req := &graphql.Request{
		Query: `
query Secret ($id: SecretID!) {
	core {
		secret(id: $id)
	}
}
`,
		Variables: map[string]any{
			"id": id,
		},
	}
	resp := struct {
		Core struct {
			Secret string
		}
	}{}
	err := client.MakeRequest(ctx, req, &graphql.Response{Data: &resp})
	if err != nil {
		return "", err
	}

	return resp.Core.Secret, nil
}
