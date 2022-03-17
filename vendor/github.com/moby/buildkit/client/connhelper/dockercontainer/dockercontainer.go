// Package dockercontainer provides connhelper for docker-container://<container>
package dockercontainer

import (
	"context"
	"net"
	"net/url"

	"github.com/docker/cli/cli/connhelper/commandconn"
	"github.com/moby/buildkit/client/connhelper"
	"github.com/pkg/errors"
)

func init() {
	connhelper.Register("docker-container", Helper)
}

// Helper returns helper for connecting to a Docker container.
// Requires BuildKit v0.5.0 or later in the container.
func Helper(u *url.URL) (*connhelper.ConnectionHelper, error) {
	sp, err := SpecFromURL(u)
	if err != nil {
		return nil, err
	}
	return &connhelper.ConnectionHelper{
		ContextDialer: func(ctx context.Context, addr string) (net.Conn, error) {
			ctxFlags := []string{}
			if sp.Context != "" {
				ctxFlags = append(ctxFlags, "--context="+sp.Context)
			}
			// using background context because context remains active for the duration of the process, after dial has completed
			return commandconn.New(context.Background(), "docker", append(ctxFlags, []string{"exec", "-i", sp.Container, "buildctl", "dial-stdio"}...)...)
		},
	}, nil
}

// Spec
type Spec struct {
	Context   string
	Container string
}

// SpecFromURL creates Spec from URL.
// URL is like docker-container://<container>?context=<context>
// Only <container> part is mandatory.
func SpecFromURL(u *url.URL) (*Spec, error) {
	q := u.Query()
	sp := Spec{
		Context:   q.Get("context"),
		Container: u.Hostname(),
	}
	if sp.Container == "" {
		return nil, errors.New("url lacks container name")
	}
	return &sp, nil
}
