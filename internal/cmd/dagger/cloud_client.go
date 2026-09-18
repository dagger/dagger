package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/mattn/go-isatty"
)

var (
	cloudOrgFlag string
	cloudJSON    bool
)

var errCloudNotAuthenticated = errors.New("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")

func (cli *CloudCLI) cloudClient(ctx context.Context) (*cloudapi.Client, *cloudauth.Cloud, error) {
	return cli.cloudClientWithLogin(ctx, true)
}

func (cli *CloudCLI) cloudClientWithLogin(ctx context.Context, login bool) (*cloudapi.Client, *cloudauth.Cloud, error) {
	cloudAuth, err := cli.cloudAuthWithLogin(ctx, login)
	if err != nil {
		return nil, nil, err
	}
	client, err := cloudapi.NewClient(ctx, cloudAuth)
	if err != nil {
		return nil, nil, fmt.Errorf("cloud client: %w", err)
	}
	return client, cloudAuth, nil
}

// cloudOTLPClient is the client for Cloud's binary OTLP stream endpoints,
// which are addressed by trace ID and token alone: a Cloud credential is
// needed (with the same interactive login fallback the GraphQL client gets),
// an org is not.
func (cli *CloudCLI) cloudOTLPClient(ctx context.Context) (*cloudapi.OTLPClient, error) {
	if cli.otlpClient != nil {
		return cli.otlpClient, nil
	}
	cloudAuth, err := cli.cloudAuthWithLogin(ctx, true)
	if err != nil {
		return nil, err
	}
	client, err := cloudapi.NewOTLPClient(ctx, cloudAuth)
	if err != nil {
		return nil, fmt.Errorf("cloud client: %w", err)
	}
	return client, nil
}

// cloudAuthWithLogin resolves the Cloud credential, logging in interactively
// when there is none and login is set (and a terminal is at hand).
func (cli *CloudCLI) cloudAuthWithLogin(ctx context.Context, login bool) (*cloudauth.Cloud, error) {
	cloudAuth, err := cloudauth.GetCloudAuth(ctx)
	if err != nil {
		cloudAuth, err = cloudAuthFromLocalTokenWithoutOrg(ctx, err)
		if err != nil {
			return nil, err
		}
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		if !login {
			return nil, errCloudNotAuthenticated
		}
		if cloudJSON {
			return nil, errCloudNotAuthenticated
		}
		// Browser-based OAuth login can't complete without a terminal to
		// echo the device code / open the URL. Without this guard the call
		// hangs forever in CI or any non-interactive context. Surface the
		// same not-authenticated error the JSON path uses; callers already
		// handle it.
		if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stderr.Fd()) {
			return nil, errCloudNotAuthenticated
		}
		if err := cloudauth.Login(ctx, os.Stderr, cloudauth.WithAuthGate()); err != nil {
			return nil, err
		}
		cloudAuth, err = cloudauth.GetCloudAuth(ctx)
		if err != nil {
			cloudAuth, err = cloudAuthFromLocalTokenWithoutOrg(ctx, err)
			if err != nil {
				return nil, err
			}
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return nil, errCloudNotAuthenticated
		}
	}
	return cloudAuth, nil
}

func cloudAuthFromLocalTokenWithoutOrg(ctx context.Context, err error) (*cloudauth.Cloud, error) {
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cloud auth: %w", err)
	}
	token, tokenErr := cloudauth.Token(ctx)
	if tokenErr != nil {
		return nil, fmt.Errorf("cloud auth: %w", tokenErr)
	}
	return &cloudauth.Cloud{Token: token}, nil
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
