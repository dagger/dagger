package cloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

// Hotline uses the same credential and refresh path as other Cloud requests.
func (c *Client) Hotline(ctx context.Context, org, method, path, key string, body io.Reader) (*http.Response, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.u.ResolveReference(ref).String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dagger-Org", org)
	req.Header.Set("Idempotency-Key", key)
	client := *c.h
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(req)
}
