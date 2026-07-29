package engineutil

import (
	"context"
	"net/http"

	dockerapi "github.com/docker/docker/api"
	dockerserver "github.com/docker/docker/api/server"
	dockermiddleware "github.com/docker/docker/api/server/middleware"
	dockerrouter "github.com/docker/docker/api/server/router"
)

func newDockerHandler(ctx context.Context, cli *Client, callerExecID string) (http.Handler, error) {
	srv := new(dockerserver.Server)

	versionMiddleware, err := dockermiddleware.NewVersionMiddleware(dockerapi.DefaultVersion, dockerapi.DefaultVersion, dockerapi.MinSupportedAPIVersion)
	if err != nil {
		return nil, err
	}
	srv.UseMiddleware(versionMiddleware)

	routers := []dockerrouter.Router{
		newDockerContainerRouter(ctx, cli, callerExecID),
		newDockerSystemRouter(ctx),
	}

	for _, r := range routers {
		for _, route := range r.Routes() {
			if exp, ok := route.(dockerrouter.ExperimentalRoute); ok {
				exp.Enable()
			}
		}
	}

	return srv.CreateMux(ctx, routers...), nil
}
