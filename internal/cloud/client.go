package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
)

type Client struct {
	Trace bool

	u *url.URL
	c *http.Client

	l sync.Mutex

	retryLogin bool
}

func NewClient(api string) (*Client, error) {
	if api == "" {
		api = "https://api.dagger.cloud"
	}

	u, err := url.Parse(api)
	if err != nil {
		return nil, err
	}

	return &Client{
		u: u,
		c: http.DefaultClient,
	}, nil
}

type CreateOrgRequest struct {
	Name string `json:"name"`
}

type CreateOrgResponse struct {
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) CreateOrg(ctx context.Context, req *CreateOrgRequest) (*CreateOrgResponse, error) {
	var res CreateOrgResponse
	if err := c.apiReq(ctx, "POST", "/orgs", req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (c *Client) apiReq(ctx context.Context, method, path string, reqType, dest any) error {
	payload, err := json.Marshal(reqType)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", c.u.JoinPath(path).String(), bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if reqType != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.doWithAuth(ctx, req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode >= 400 {
		return fmt.Errorf("bad response: %s", res.Status)
	}

	if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	return nil
}

func (c *Client) doWithAuth(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)

	// OAuth2 authentication
	if err := auth.SetAuthHeader(ctx, req); err != nil {
		// if token is invalid or expired, we should handle re-auth in sync
		// fashion.
		c.l.Lock()
		defer c.l.Unlock()

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

	// req.Header.Set("User-Agent", version.Long())

	if c.Trace {
		req.Header.Write(os.Stderr)
		fmt.Fprintln(os.Stderr)
	}

	resp, err := c.c.Do(req)
	if err != nil {
		return resp, err
	}

	if c.Trace {
		resp.Header.Write(os.Stderr)
		fmt.Fprintln(os.Stderr)
	}

	// If there's an auth problem, login and retry the request
	if resp.StatusCode == http.StatusUnauthorized && c.retryLogin {
		c.retryLogin = false
		err := auth.Login(ctx)
		if err != nil {
			return resp, err
		}
		return c.doWithAuth(ctx, req)
	}

	return resp, err
}
