package engineconn

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
)

func FromSessionEnv() (EngineConn, bool, error) {
	urlStr, ok := os.LookupEnv("DAGGER_SESSION_URL")
	if !ok {
		return nil, false, nil
	}

	sessionToken := os.Getenv("DAGGER_SESSION_TOKEN")
	if sessionToken == "" {
		return nil, false, fmt.Errorf("DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_URL")
	}
	url, err := url.Parse(urlStr)
	if err != nil {
		return nil, false, fmt.Errorf("invalid DAGGER_SESSION_URL: %w", err)
	}

	httpClient := defaultHTTPClient(&ConnectParams{
		Host:         url.Host,
		SessionToken: sessionToken,
	})

	return &sessionEnvConn{
		Client: httpClient,
		host:   url.Host,
	}, true, nil
}

type sessionEnvConn struct {
	*http.Client
	host string
}

func (c *sessionEnvConn) Host() string {
	return c.host
}

func (c *sessionEnvConn) Close() error {
	return nil
}
