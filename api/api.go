package api

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/api/auth"
	"go.dagger.io/dagger/version"
)

// Client is the API client
type Client struct {
	c *http.Client

	// retryLogin will retry to login once (and only once) upon receiving an
	// "Unauthorized" response from the API server.
	retryLogin bool
}

// New creates a new API client
func New() *Client {
	return &Client{
		c:          &http.Client{},
		retryLogin: true,
	}
}

// Do fires a request
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)

	// OAuth2 authentication
	if err := auth.SetAuthHeader(ctx, req); err != nil {
		// If we fail to refresh an access token, try to log in again.
		c.retryLogin = false
		err := auth.Login(ctx)
		if err != nil {
			return nil, err
		}
		if err := auth.SetAuthHeader(ctx, req); err != nil {
			return nil, err
		}
	}

	req.Header.Set("User-Agent", version.Long())

	resp, err := c.c.Do(req)
	if err != nil {
		return resp, err
	}

	// If there's an auth problem, login and retry the request
	if resp.StatusCode == http.StatusUnauthorized && c.retryLogin {
		c.retryLogin = false
		err := auth.Login(ctx)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Msg("error authenticating")
			return resp, err
		}
		return c.Do(ctx, req)
	}

	return resp, err
}
