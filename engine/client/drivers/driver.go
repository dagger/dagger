package drivers

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

type Driver interface {
	// Provision creates any underlying resources for a driver, and returns a
	// Connector that can connect to it.
	Provision(ctx context.Context, url *url.URL, opts *DriverOpts) (Connector, error)
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
