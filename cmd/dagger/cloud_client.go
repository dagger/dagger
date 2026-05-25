package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

var (
	cloudOrgFlag string
	cloudJSON    bool
)

func (cli *CloudCLI) cloudClient(ctx context.Context) (*cloudapi.Client, *cloudauth.Cloud, error) {
	return cli.cloudClientWithLogin(ctx, true)
}

func (cli *CloudCLI) cloudClientNoLogin(ctx context.Context) (*cloudapi.Client, *cloudauth.Cloud, error) {
	return cli.cloudClientWithLogin(ctx, false)
}

func (cli *CloudCLI) cloudClientWithLogin(ctx context.Context, login bool) (*cloudapi.Client, *cloudauth.Cloud, error) {
	cloudAuth, err := cloudauth.GetCloudAuth(ctx)
	if err != nil {
		token, tokenErr := cloudauth.Token(ctx)
		if tokenErr != nil {
			return nil, nil, fmt.Errorf("cloud auth: %w", err)
		}
		cloudAuth = &cloudauth.Cloud{Token: token}
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		if !login {
			return nil, nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
		}
		if cloudJSON {
			return nil, nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
		}
		if err := cloudauth.Login(ctx, os.Stderr); err != nil {
			return nil, nil, err
		}
		cloudAuth, err = cloudauth.GetCloudAuth(ctx)
		if err != nil {
			token, tokenErr := cloudauth.Token(ctx)
			if tokenErr != nil {
				return nil, nil, fmt.Errorf("cloud auth: %w", err)
			}
			cloudAuth = &cloudauth.Cloud{Token: token}
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return nil, nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
		}
	}

	client, err := cloudapi.NewClient(ctx, cloudAuth)
	if err != nil {
		return nil, nil, fmt.Errorf("cloud client: %w", err)
	}
	return client, cloudAuth, nil
}

func (cli *CloudCLI) resolveCloudOrg(ctx context.Context, client *cloudapi.Client, cloudAuth *cloudauth.Cloud) (*cloudapi.OrgResponse, error) {
	orgName := cloudOrgFlag
	if orgName == "" && cloudAuth.Org != nil {
		orgName = cloudAuth.Org.Name
	}
	if orgName == "" {
		if currentOrgName, err := cloudauth.CurrentOrgName(); err == nil {
			orgName = currentOrgName
		}
	}
	user, userErr := client.User(ctx)
	if orgName == "" && userErr == nil {
		if len(user.Orgs) == 1 {
			orgName = user.Orgs[0].Name
			_ = cloudauth.SetCurrentOrg(&user.Orgs[0])
		}
	}
	if orgName == "" {
		return nil, fmt.Errorf("no org specified; use --org or run 'dagger login <org>'")
	}

	if userErr == nil && !userHasOrg(user, orgName) {
		return nil, fmt.Errorf("org %q is not available for the current account; use --org or run 'dagger login <org>'", orgName)
	}

	org, err := client.OrgByName(ctx, orgName)
	if err != nil {
		return nil, fmt.Errorf("resolve org %q: %w", orgName, err)
	}
	return org, nil
}

func userHasOrg(user *cloudapi.UserResponse, orgName string) bool {
	for _, org := range user.Orgs {
		if strings.EqualFold(org.Name, orgName) {
			return true
		}
	}
	return false
}
