package api

import (
	"context"
	"net/http"
	"sync"
	"time"

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
		c: &http.Client{
			Timeout: 10 * time.Second,
		},
		retryLogin: true,
	}
}

// global mutex across all telemetry clients.
var m sync.Mutex

// Do fires a request
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)

	// OAuth2 authentication
	if err := auth.SetAuthHeader(ctx, req); err != nil {
		// if token is invalid or expired, we should handle re-auth in sync
		// fashion.
		m.Lock()
		defer m.Unlock()

		// only client trying to re-auth. other waiting clients shouldn't
		// re-trigger auth
		if c.retryLogin {
			// If we fail to refresh an access token, try to log in again.
			c.retryLogin = false
			err := auth.Login(ctx)
			if err != nil {
				return nil, err
			}
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
