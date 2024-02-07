package drivers

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/pkg/errors"
	"github.com/vito/progrock"

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

func (d *dialDriver) Connect(ctx context.Context, rec *progrock.VertexRecorder, target *url.URL, _ *DriverOpts) (c net.Conn, rerr error) {
	startTask := rec.Task("starting engine")
	defer startTask.Done(rerr)

	return d.dial(ctx, target)
}

func (d *dialDriver) dial(ctx context.Context, target *url.URL) (net.Conn, error) {
	if d.fn == nil {
		return defaultDialer(ctx, target.String())
	}

	helper, err := d.fn(target)
	if err != nil {
		return nil, err
	}
	return helper.ContextDialer(ctx, target.String())
}

func defaultDialer(ctx context.Context, address string) (net.Conn, error) {
	addrParts := strings.SplitN(address, "://", 2)
	if len(addrParts) != 2 {
		return nil, errors.Errorf("invalid address %s", address)
	}
	var d net.Dialer
	return d.DialContext(ctx, addrParts[0], addrParts[1])
}
