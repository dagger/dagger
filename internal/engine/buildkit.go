package engine

import (
	"context"
	"net/url"
)

func remoteBuildkitProvider(ctx context.Context, remote *url.URL) (string, error) {
	return "tcp://" + remote.Host, nil
}
