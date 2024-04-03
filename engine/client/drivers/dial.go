package drivers

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/pkg/errors"

	connh "github.com/moby/buildkit/client/connhelper"
	connhDocker "github.com/moby/buildkit/client/connhelper/dockercontainer"
	connhKube "github.com/moby/buildkit/client/connhelper/kubepod"
	connhPodman "github.com/moby/buildkit/client/connhelper/podmancontainer"
	connhSSH "github.com/moby/buildkit/client/connhelper/ssh"
)

func init() {
	register("tcp", &dialDriver{})
	register("unix", &dialDriver{})
	register("ssh", &dialDriver{connhSSH.Helper})
	register("docker-container", &dialDriver{connhDocker.Helper})
	register("kube-pod", &dialDriver{connhKube.Helper})
	register("podman-container", &dialDriver{connhPodman.Helper})
}

// dialDriver uses the buildkit connhelpers to directly connect
type dialDriver struct {
	fn func(*url.URL) (*connh.ConnectionHelper, error)
}

func (d *dialDriver) Provision(ctx context.Context, target *url.URL, _ *DriverOpts) (Connector, error) {
	return dialConnector{dialDriver: d, target: target}, nil
}

type dialConnector struct {
	*dialDriver
	target *url.URL
}

func (d dialConnector) Connect(ctx context.Context) (_ net.Conn, rerr error) {
	if d.fn == nil {
		return defaultDialer(ctx, d.target.String())
	}

	helper, err := d.fn(d.target)
	if err != nil {
		return nil, err
	}
	return helper.ContextDialer(ctx, d.target.String())
}

func defaultDialer(ctx context.Context, address string) (net.Conn, error) {
	addrParts := strings.SplitN(address, "://", 2)
	if len(addrParts) != 2 {
		return nil, errors.Errorf("invalid address %s", address)
	}
	var d net.Dialer
	return d.DialContext(ctx, addrParts[0], addrParts[1])
}
