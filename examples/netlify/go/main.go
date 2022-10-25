package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"

	openAPIClient "github.com/go-openapi/runtime/client"
	netlifyModel "github.com/netlify/open-api/v2/go/models"
	netlifyOps "github.com/netlify/open-api/v2/go/plumbing/operations"
	netlifyClient "github.com/netlify/open-api/v2/go/porcelain"
	netlifycontext "github.com/netlify/open-api/v2/go/porcelain/context"
)

func main() {
	dagger.Serve(Netlify{})
}

type Netlify struct {
}

func (Netlify) Deploy(ctx context.Context, contents dagger.DirectoryID, subdir *string, siteName *string, token dagger.SecretID) (*SiteURLs, error) {
	client, err := dagger.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// Setup Auth
	tokenPlaintext, err := client.Secret(token).Plaintext(ctx)
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

type SiteURLs struct {
	URL       string
	DeployURL string
	LogsURL   string
}
