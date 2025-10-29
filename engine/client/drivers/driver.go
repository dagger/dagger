package drivers

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/dagger/dagger/engine/client/imageload"
)

type Driver interface {
	// Available returns true if the driver backend is running and available for use.
	Available(ctx context.Context) (bool, error)

	// Provision creates any underlying resources for a driver, and returns a
	// Connector that can connect to it.
	Provision(ctx context.Context, url *url.URL, opts *DriverOpts) (Connector, error)

	// ImageLoader returns an optional associated image loader - not all
	// drivers will have this!
	ImageLoader(ctx context.Context) imageload.Backend
}

type Connector interface {
	// Connect creates a connection to a dagger instance.
	//
	// Connect can be called multiple times during attempts to establish a
	// connection - but a connector can choose to block this call until
	// previously returned connections have been closed.
	Connect(ctx context.Context) (net.Conn, error)
}

type DriverOpts struct {
	DaggerCloudToken string
	GPUSupport       string
	Module           string
	Function         string
	ExecCmd          []string
	ClientID         string
}

const (
	EnvDaggerCloudToken = "DAGGER_CLOUD_TOKEN"
	EnvGPUSupport       = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)

var drivers = map[string][]Driver{}

func register(scheme string, driver ...Driver) {
	drivers[scheme] = driver
}

func GetDriver(ctx context.Context, name string) (Driver, error) {
	drivers, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("no driver for scheme %q found", name)
	}
	for _, driver := range drivers {
		available, err := driver.Available(ctx)
		if err != nil {
			return nil, err
		}
		if available {
			return driver, nil
		}
	}
	return nil, fmt.Errorf("driver for scheme %q was not available", name)
}
