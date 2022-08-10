package main

import (
	"context"
	"encoding/json"
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

func (r *netlifyResolver) Deploy(ctx context.Context, obj *Netlify, contents dagger.FSID, subdir *string, siteName *string, token dagger.SecretID) (*Deploy, error) {
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

func (r *deployResolver) URL(ctx context.Context, obj *Deploy) (string, error) {

	return obj.URL, nil

}

func (r *deployResolver) DeployURL(ctx context.Context, obj *Deploy) (string, error) {

	return obj.DeployURL, nil

}

func (r *deployResolver) LogsURL(ctx context.Context, obj *Deploy) (*string, error) {

	return obj.LogsURL, nil

}

func (r *queryResolver) Netlify(ctx context.Context) (*Netlify, error) {

	return new(Netlify), nil

}

type deployResolver struct{}
type netlifyResolver struct{}
type queryResolver struct{}

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){
		"Deploy.url": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployResolver{}).URL(ctx,

				obj,
			)
		},
		"Deploy.deployURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployResolver{}).DeployURL(ctx,

				obj,
			)
		},
		"Deploy.logsURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deployResolver{}).LogsURL(ctx,

				obj,
			)
		},
		"Netlify.deploy": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			var contents dagger.FSID

			bytes, err = json.Marshal(fc.Args["contents"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &contents); err != nil {
				return nil, err
			}

			var subdir string

			bytes, err = json.Marshal(fc.Args["subdir"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &subdir); err != nil {
				return nil, err
			}

			var siteName string

			bytes, err = json.Marshal(fc.Args["siteName"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &siteName); err != nil {
				return nil, err
			}

			var token dagger.SecretID

			bytes, err = json.Marshal(fc.Args["token"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &token); err != nil {
				return nil, err
			}

			obj := new(Netlify)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&netlifyResolver{}).Deploy(ctx,

				obj,

				contents,

				&subdir,

				&siteName,

				token,
			)
		},
		"Query.netlify": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			return (&queryResolver{}).Netlify(ctx)
		},
	})
}
