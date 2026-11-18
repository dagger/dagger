package drivers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/internal/cloud/auth"
)

type Driver interface {
	// Available reports whether the driver can serve the given URL, judging by
	// the URL itself (its target and query parameters) and not only by whether
	// the driver's backend is installed. Return false with a nil error to
	// defer to the next driver registered for the scheme; wrap an
	// *UnavailableError to defer while recording why this driver cannot serve
	// the URL; return any other error to stop the search.
	Available(ctx context.Context, u *url.URL) (bool, error)

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

	// If available, the identifier of the claim on the engine that its
	// provisioner issued with it, otherwise empty string.
	ClaimID() string
}

type DriverOpts struct {
	DaggerCloudToken string
	GPUSupport       string
	Module           string
	Function         string
	ExecCmd          []string
	GitRemote        string
	WorkspacePath    string
	ClientID         string
	CloudAuth        *auth.Cloud
}

const (
	EnvDaggerCloudToken = "DAGGER_CLOUD_TOKEN"
	EnvGPUSupport       = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)

var drivers = map[string][]Driver{}

func register(scheme string, driver ...Driver) {
	drivers[scheme] = driver
}

// GetDriver returns the first driver registered for the URL's scheme that is
// available to serve the URL.
func GetDriver(ctx context.Context, u *url.URL) (Driver, error) {
	name := u.Scheme
	drivers, ok := drivers[name]
	if !ok {
		// Teach the usable values here: an engine selector is often the first
		// thing a user gets wrong, and the message is the only help they see.
		return nil, fmt.Errorf("no driver for scheme %q found; supported schemes: %s",
			name, strings.Join(RegisteredSchemes(), ", "))
	}
	var unavailable []string
	for _, driver := range drivers {
		available, err := driver.Available(ctx, u)
		if err != nil {
			if reason, ok := errors.AsType[*UnavailableError](err); ok {
				unavailable = append(unavailable, reason.Reason)
				continue
			}
			return nil, err
		}
		if available {
			return driver, nil
		}
	}
	if len(unavailable) > 0 {
		return nil, fmt.Errorf("no driver is available for %s://%s:\n  - %s\n\nTo choose a different engine, run `dagger help engine`",
			name, u.Host, strings.Join(unavailable, "\n  - "))
	}
	return nil, &runtimeUnavailableError{
		err: fmt.Errorf("no container runtime is available for engine scheme %q", name),
		message: "Dagger needs a container runtime to start the local engine.\n\n" +
			"Install a compatible container runtime and make sure its command is in PATH.\n" +
			"To choose a local or remote engine, run `dagger help engine`.",
	}
}
