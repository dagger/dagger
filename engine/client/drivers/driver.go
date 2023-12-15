package drivers

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/vito/progrock"
)

// Driver allows dialing to a dagger backend.
//
// It's slightly similar to the docker connhelpers, however, the function
// signatures are slightly different.
type Driver interface {
	// Connect creates a buildkit client to a dagger instance from the given URL
	Connect(ctx context.Context, rec *progrock.VertexRecorder, url *url.URL, opts *DriverOpts) (net.Conn, error)
}

type DriverOpts struct {
	UserAgent string

	DaggerCloudToken string
	GPUSupport       string
}

const (
	EnvDaggerCloudToken = "DAGGER_CLOUD_TOKEN"
	EnvGPUSupport       = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)

var drivers = map[string]Driver{}

func register(scheme string, driver Driver) {
	drivers[scheme] = driver
}

func GetDriver(name string) (Driver, error) {
	driver, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("no driver for scheme %q found", name)
	}
	return driver, nil
}
