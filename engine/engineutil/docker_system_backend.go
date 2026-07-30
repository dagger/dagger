package engineutil

import (
	"context"
	"errors"
	"time"

	dockerrouter "github.com/docker/docker/api/server/router"
	dockersystem "github.com/docker/docker/api/server/router/system"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/backend"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/system"
)

func newDockerSystemRouter(_ context.Context) dockerrouter.Router {
	return dockersystem.NewRouter(new(dockerSystemBackend), new(dockerSystemClusterBackend), new(dockerSystemBuildBackend), func() map[string]bool {
		return map[string]bool{}
	})
}

type dockerSystemBackend struct{}

var _ dockersystem.Backend = (*dockerSystemBackend)(nil)

var (
	errSystemUnimplemented = errors.New("docker system backend: not implemented")
)

// AuthenticateToRegistry implements [system.Backend].
func (d *dockerSystemBackend) AuthenticateToRegistry(ctx context.Context, authConfig *registry.AuthConfig) (string, string, error) {
	return "", "", errSystemUnimplemented
}

// SubscribeToEvents implements [system.Backend].
func (d *dockerSystemBackend) SubscribeToEvents(since time.Time, until time.Time, ef filters.Args) ([]events.Message, chan interface{}) {
	panic(errSystemUnimplemented.Error())
}

// SystemDiskUsage implements [system.Backend].
func (d *dockerSystemBackend) SystemDiskUsage(ctx context.Context, opts backend.DiskUsageOptions) (*backend.DiskUsage, error) {
	return nil, errSystemUnimplemented
}

// SystemInfo implements [system.Backend].
func (d *dockerSystemBackend) SystemInfo(context.Context) (*system.Info, error) {
	return nil, errSystemUnimplemented
}

// SystemVersion implements [system.Backend].
func (d *dockerSystemBackend) SystemVersion(context.Context) (types.Version, error) {
	return types.Version{}, errSystemUnimplemented
}

// UnsubscribeFromEvents implements [system.Backend].
func (d *dockerSystemBackend) UnsubscribeFromEvents(chan interface{}) {
	panic(errSystemUnimplemented.Error())
}

type dockerSystemClusterBackend struct{}

var _ dockersystem.ClusterBackend = (*dockerSystemClusterBackend)(nil)

// Info implements [system.ClusterBackend].
func (d *dockerSystemClusterBackend) Info(context.Context) swarm.Info {
	panic(errSystemUnimplemented.Error())
}

type dockerSystemBuildBackend struct{}

var _ dockersystem.BuildBackend = (*dockerSystemBuildBackend)(nil)

// DiskUsage implements [system.BuildBackend].
func (d *dockerSystemBuildBackend) DiskUsage(context.Context) ([]*build.CacheRecord, error) {
	return nil, errSystemUnimplemented
}
