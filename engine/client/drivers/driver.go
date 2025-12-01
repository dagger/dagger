package drivers

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/internal/cloud/auth"
)

type Driver interface {
	// Available returns true if the driver backend is running and available for use.
	// Deprecated: only used by deprecated GetDriver()
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

	// If available, a unique identifier for the engine, otherwise empty string.
	EngineID() string
}

type DriverOpts struct {
	DaggerCloudToken string
	GPUSupport       string
	Module           string
	Function         string
	ExecCmd          []string
	ClientID         string
	CloudAuth        *auth.Cloud
}

const (
	EnvDaggerCloudToken = "DAGGER_CLOUD_TOKEN"
	EnvGPUSupport       = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)

var driversByScheme = map[string][]Driver{}

func register(scheme string, driver ...Driver) {
	driversByScheme[scheme] = driver
}

// GetDrivers returns the list of drivers irrespective of whether they are Available.
// Loop through them until a Connector returned by Provision() is able to Connect().
func GetDrivers(ctx context.Context, scheme string) ([]Driver, error) {
	drivers, ok := driversByScheme[scheme]
	if !ok {
		return nil, fmt.Errorf("no driver for scheme %q found", scheme)
	}
	return drivers, nil
}

// Deprecated: GetDriver returns a Driver that is available as indicated by Driver.Available()
// Use GetDrivers() instead.
func GetDriver(ctx context.Context, scheme string) (Driver, error) {
	drivers, err := GetDrivers(ctx, scheme)
	if err != nil {
		return nil, err
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
	return nil, fmt.Errorf("driver for scheme %q was not available", scheme)
}
