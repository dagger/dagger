package engineconn

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
)

func FromSessionEnv() (EngineConn, bool, error) {
	nesting := os.Getenv("DAGGER_NESTING")
	portStr, ok := os.LookupEnv("DAGGER_SESSION_PORT")
	switch nesting {
	case "":
		// Missing marker preserves the legacy nested-client behavior.
	case "NESTED_CLIENT":
		if !ok {
			return nil, false, fmt.Errorf("DAGGER_NESTING=NESTED_CLIENT requires DAGGER_SESSION_PORT")
		}
	case "INDEPENDENT_SESSIONS":
		if !ok {
			return nil, false, fmt.Errorf("DAGGER_NESTING=INDEPENDENT_SESSIONS requires DAGGER_SESSION_PORT")
		}
		if _, err := strconv.Atoi(portStr); err != nil {
			return nil, false, fmt.Errorf("invalid port in DAGGER_SESSION_PORT: %w", err)
		}
		// Independent mode provisions the ordinary local CLI helper. The helper
		// inherits this port and marker and creates fresh client identities.
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("unknown DAGGER_NESTING value %q", nesting)
	}
	if !ok {
		return nil, false, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, false, fmt.Errorf("invalid port in DAGGER_SESSION_PORT: %w", err)
	}

	sessionToken := os.Getenv("DAGGER_SESSION_TOKEN")
	if sessionToken == "" {
		return nil, false, fmt.Errorf("DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT")
	}

	httpClient := defaultHTTPClient(&ConnectParams{
		Port:         port,
		SessionToken: sessionToken,
	})

	return &sessionEnvConn{
		Client: httpClient,
		host:   fmt.Sprintf("127.0.0.1:%d", port),
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
